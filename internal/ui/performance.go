package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/buildinfo"
	"narutotimer/internal/config"
	"narutotimer/internal/diagnostics"
	"narutotimer/internal/frame"
)

// Only this public contract is used. The interface also lets lifecycle tests
// block initialization, Stop and Export without starting platform collectors.
type performanceRecorder interface {
	Snapshot() diagnostics.PerformanceStatus
	Metadata() diagnostics.PerformanceMetadata
	ObserveFrame(frame.Frame, time.Time, time.Duration, bool)
	ObserveLoop(total, post time.Duration)
	ObserveUIQueue(time.Duration)
	Mark(string) error
	Stop() error
	Done() <-chan struct{}
	Export(context.Context) (string, error)
}

type performancePhase uint8

const (
	performanceStarting performancePhase = iota
	performanceRunning
	performanceStopping
	performanceStopped
	performanceFailed
)

// All mutable run fields are protected by performanceMu. recorder is immutable
// after publication through performanceActive, so capture needs only an atomic
// load. notify never closes: an in-flight OnSample may still send during Stop.
type performanceRun struct {
	recorder        performanceRecorder
	ctx             context.Context
	cancel          context.CancelFunc
	notify          chan struct{}
	stop            chan struct{}
	stopOnce        sync.Once
	phase           performancePhase
	profiles        bool
	status          diagnostics.PerformanceStatus
	metadata        diagnostics.PerformanceMetadata
	exporting       bool
	marking         bool
	exportAfterStop bool
	exportOnExit    bool // The app is closing: write the report before the process ends.
	errorText       string
	notice          string
	exportPath      string
	exportID        uint64
}

// Zero value is off. Every worker admission (including a pending constructor)
// is registered under performanceMu. Shutdown closes admission under that same
// lock before Run waits; workers never wait for a Fyne callback to finish.
type performanceState struct {
	performanceMu       sync.Mutex
	performanceCurrent  *performanceRun
	performanceClosing  bool
	performanceWorkers  sync.WaitGroup
	performanceActive   atomic.Pointer[performanceRun]
	performancePending  latestCoalescer[performanceUpdate]
	performanceExportID uint64

	// "Previous session" export state, protected by performanceMu.
	performanceOffline       bool
	performanceOfflineText   string
	performanceOfflineCancel context.CancelFunc
	performanceOfflineExport uint64
	performanceOfflinePath   string

	// Fyne-thread-only objects. Closing the view does not stop the recorder.
	// performanceButton is the overlay's settings button, which doubles as
	// the "recording" indicator. performanceSeenExport lives here, not in the
	// view, so a report finished while Settings is closed is still announced.
	performanceView       *performanceView
	performanceButton     *widget.Button
	performanceSeenExport uint64
	// Capture-loop-goroutine only: when the current frame reached the loop.
	performanceReceived time.Time

	// Immutable after session initialization; nil selects production behavior.
	performanceNew      func(diagnostics.PerformanceOptions) (performanceRecorder, error)
	performancePost     func(func())
	performanceExported func(fyne.Window, string)
}

type performanceUpdate struct {
	run             *performanceRun
	recorder        performanceRecorder
	phase           performancePhase
	status          diagnostics.PerformanceStatus
	metadata        diagnostics.PerformanceMetadata
	exporting       bool
	marking         bool
	errorText       string
	notice          string
	exportPath      string
	exportID        uint64
	fullDiagnostics bool
	offline         bool
	offlineText     string
}

func performanceSettings(cfg config.Config) diagnostics.PerformanceSettings {
	return diagnostics.PerformanceSettings{
		CaptureProvider: captureProvider(cfg.Capture), PollMS: cfg.UI.PollIntervalMS,
		IdlePollMS: cfg.UI.IdlePollIntervalMS, CaptureTimeoutMS: cfg.Capture.TimeoutMS,
		ContentMode: cfg.Layout.ContentMode, Calibrated: cfg.Layout.Calibration != nil,
		OCREnabled: cfg.UI.AutoTextRecognition, PreMatchOCR: cfg.UI.PreMatchSideRecognition,
		OCRGPURequested: cfg.UI.GPUAcceleration, OverlayMode: cfg.UI.OverlayMode,
	}
}

func (s *session) startPerformance(profiles bool) bool {
	s.mu.Lock()
	options := diagnostics.PerformanceOptions{Root: s.cfg.Debug.Directory, Version: buildinfo.Version,
		Settings: performanceSettings(s.cfg), Profiles: profiles}
	// The effective settings travel with the report, so the developer does
	// not have to ask for them separately.
	options.Config, _ = json.MarshalIndent(s.cfg, "", "  ")
	s.mu.Unlock()
	s.performanceMu.Lock()
	old := s.performanceCurrent
	if s.performanceClosing || s.stopped() || s.performanceOffline || (old != nil &&
		(old.phase != performanceStopped && old.phase != performanceFailed || old.exporting || old.exportAfterStop || old.marking)) {
		s.performanceMu.Unlock()
		return false
	}
	if old != nil {
		old.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &performanceRun{ctx: ctx, cancel: cancel, notify: make(chan struct{}, 1), stop: make(chan struct{}),
		phase: performanceStarting, profiles: profiles}
	// The constructor may notify before it returns. No recorder/UI access here.
	options.OnSample = func() {
		select {
		case r.notify <- struct{}{}:
		default:
		}
	}
	s.performanceCurrent = r
	s.performanceWorkers.Add(1)
	s.performanceMu.Unlock()
	s.queuePerformanceUpdate(r)
	go s.runPerformance(r, options)
	return true
}

func (s *session) runPerformance(r *performanceRun, options diagnostics.PerformanceOptions) {
	defer s.performanceWorkers.Done()
	factory := s.performanceNew
	if factory == nil {
		factory = func(o diagnostics.PerformanceOptions) (performanceRecorder, error) {
			return diagnostics.NewPerformance(o)
		}
	}
	recorder, err := factory(options)
	if err == nil && recorder == nil {
		err = errors.New("性能采样器未创建")
	}
	if err != nil {
		if recorder != nil {
			_ = recorder.Stop()
		}
		s.performanceMu.Lock()
		r.phase, r.errorText = performanceFailed, err.Error()
		r.exportAfterStop = false // No recorder exists to export; allow a retry.
		s.performanceMu.Unlock()
		s.queuePerformanceUpdate(r)
		return
	}
	metadata, status := recorder.Metadata(), recorder.Snapshot()
	s.performanceMu.Lock()
	r.recorder, r.metadata, r.status = recorder, metadata, status
	if !s.performanceClosing && !s.stopped() && r.phase == performanceStarting && status.Active {
		r.phase = performanceRunning
		s.performanceActive.Store(r)
	} else {
		r.phase = performanceStopping
		r.stopOnce.Do(func() { close(r.stop) })
	}
	s.performanceMu.Unlock()
	s.queuePerformanceUpdate(r)
	for {
		// Stop has priority over repeated sample notifications.
		select {
		case <-r.stop:
			s.finishPerformance(r)
			return
		default:
		}
		select {
		case <-r.stop:
			s.finishPerformance(r)
			return
		case <-recorder.Done():
			s.finishPerformance(r) // also handles automatic limit/error stops
			return
		case <-r.notify:
			status, metadata := recorder.Snapshot(), recorder.Metadata()
			s.performanceMu.Lock()
			r.status, r.metadata = status, metadata
			if !status.Active && r.phase == performanceRunning {
				r.phase = performanceStopping
				s.performanceActive.CompareAndSwap(r, nil)
			}
			s.performanceMu.Unlock()
			s.queuePerformanceUpdate(r)
		}
	}
}

func (s *session) finishPerformance(r *performanceRun) {
	s.performanceActive.CompareAndSwap(r, nil)
	err := r.recorder.Stop() // includes final profiles/collector close; never on UI
	status := r.recorder.Snapshot()
	s.performanceMu.Lock()
	r.phase, r.status = performanceStopped, status
	if err != nil {
		r.errorText = "停止监测：" + err.Error()
	}
	export, onExit := r.exportAfterStop, r.exportOnExit
	s.performanceMu.Unlock()
	if onExit {
		// Quitting is how a gaming session normally ends. Leave a finished
		// report behind instead of raw samples; Run waits for this worker.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = r.recorder.Export(ctx)
		return
	}
	s.queuePerformanceUpdate(r)
	if export {
		s.exportPerformanceRun(r)
	}
}

func (s *session) stopPerformance(export bool) bool {
	s.performanceMu.Lock()
	r := s.performanceCurrent
	if s.performanceClosing || s.stopped() || r == nil || (r.phase != performanceStarting && r.phase != performanceRunning) {
		s.performanceMu.Unlock()
		return false
	}
	r.phase, r.exportAfterStop = performanceStopping, export
	s.performanceActive.CompareAndSwap(r, nil)
	r.stopOnce.Do(func() { close(r.stop) })
	s.performanceMu.Unlock()
	s.queuePerformanceUpdate(r)
	return true
}

// Called by session.stop, before Run waits for performanceWorkers. Even a
// constructor returning after shutdown must Stop its newly created recorder.
func (s *session) shutdownPerformance() {
	s.performanceMu.Lock()
	s.performanceClosing = true
	s.performanceActive.Store(nil)
	if r := s.performanceCurrent; r != nil {
		// A run still recording (or stopping toward its report) gets its ZIP
		// written on the way out; anything else in flight is canceled.
		r.exportOnExit = r.recorder != nil && (r.phase == performanceRunning || r.phase == performanceStopping && r.exportAfterStop)
		r.cancel() // cancel accepted exports, not just the visible window
		r.exportAfterStop = false
		r.stopOnce.Do(func() { close(r.stop) })
	}
	if s.performanceOfflineCancel != nil {
		s.performanceOfflineCancel()
	}
	s.performanceMu.Unlock()
}

func (s *session) exportPerformance() bool {
	s.performanceMu.Lock()
	r := s.performanceCurrent
	s.performanceMu.Unlock()
	return s.exportPerformanceRun(r)
}

func (s *session) exportPerformanceRun(r *performanceRun) bool {
	s.performanceMu.Lock()
	if s.performanceClosing || s.stopped() || r == nil || s.performanceCurrent != r || r.recorder == nil ||
		r.exporting || (r.phase != performanceRunning && r.phase != performanceStopped) {
		s.performanceMu.Unlock()
		return false
	}
	r.exporting = true
	if r.phase == performanceStopped {
		r.exportAfterStop = false
	}
	if strings.HasPrefix(r.errorText, "导出失败：") {
		r.errorText = ""
	}
	s.performanceWorkers.Add(1)
	s.performanceMu.Unlock()
	s.queuePerformanceUpdate(r)
	go func() {
		defer s.performanceWorkers.Done()
		path, err := r.recorder.Export(r.ctx)
		s.performanceMu.Lock()
		r.exporting = false
		if err != nil {
			r.errorText = "导出失败：" + err.Error()
		} else {
			s.performanceExportID++
			r.exportID, r.exportPath = s.performanceExportID, path
			r.notice = "报告已生成。"
		}
		// A stop-and-export requested during a snapshot export gets a FINAL
		// export after Stop, without concurrent ZIP writers or blocking the UI.
		again := r.exportAfterStop && r.phase == performanceStopped
		s.performanceMu.Unlock()
		s.queuePerformanceUpdate(r)
		if again {
			s.exportPerformanceRun(r)
		}
	}()
	return true
}

func (s *session) markPerformanceStall() bool {
	s.performanceMu.Lock()
	r := s.performanceCurrent
	if s.performanceClosing || s.stopped() || r == nil || r.phase != performanceRunning || !r.status.Active || r.marking {
		s.performanceMu.Unlock()
		return false
	}
	r.marking = true
	s.performanceWorkers.Add(1)
	s.performanceMu.Unlock()
	s.queuePerformanceUpdate(r)
	go func() {
		defer s.performanceWorkers.Done()
		err := r.recorder.Mark(diagnostics.PerformanceUserMark + "：卡顿")
		s.performanceMu.Lock()
		r.marking = false
		if err != nil {
			r.errorText = "标记失败：" + err.Error()
		} else {
			r.notice = "已标记卡顿 · " + time.Now().Format("15:04:05")
		}
		s.performanceMu.Unlock()
		s.queuePerformanceUpdate(r)
	}()
	return true
}

// One atomic load when off. Never enqueue a Frame/image or nest the session,
// diagnostics and performance locks. The recorder reads the frame's timings,
// labels and error text synchronously and retains no pixels.
func (s *session) observePerformanceFrame(f frame.Frame) {
	r := s.performanceActive.Load()
	if r == nil || s.stopped() {
		return
	}
	select {
	case <-r.stop:
		return
	case <-r.recorder.Done():
		return
	default:
	}
	s.performanceReceived = time.Now()
	r.recorder.ObserveFrame(f, s.performanceReceived, s.poll(), s.currentDiagnostics() != nil)
}

// observePerformanceLoop reports one whole capture-loop iteration: its total
// time and the part after the frame arrived (state update, bead logic, overlay
// post). Called from the capture loop only.
func (s *session) observePerformanceLoop(started time.Time) {
	r := s.performanceActive.Load()
	received := s.performanceReceived
	s.performanceReceived = time.Time{}
	if r == nil || received.IsZero() || received.Before(started) {
		return
	}
	now := time.Now()
	r.recorder.ObserveLoop(now.Sub(started), now.Sub(received))
}

// Caller holds performanceMu. Snapshot/Metadata copies have already been made
// off the Fyne thread and are never mutated after publication.
func (s *session) performanceUpdateLocked(full bool) *performanceUpdate {
	u := &performanceUpdate{fullDiagnostics: full}
	if r := s.performanceCurrent; r != nil {
		u.run, u.recorder, u.phase, u.status, u.metadata = r, r.recorder, r.phase, r.status, r.metadata
		// Keep restart disabled across Stop completion -> final export admission.
		// Otherwise a new run could replace the old one and lose the requested ZIP.
		u.exporting = r.exporting || (r.exportAfterStop && r.phase == performanceStopped)
		u.marking, u.errorText, u.notice = r.marking, r.errorText, r.notice
		u.exportPath, u.exportID = r.exportPath, r.exportID
	}
	u.offline, u.offlineText = s.performanceOffline, s.performanceOfflineText
	if s.performanceOfflineExport > u.exportID {
		u.exportID, u.exportPath = s.performanceOfflineExport, s.performanceOfflinePath
	}
	return u
}

func (s *session) queuePerformanceUpdate(r *performanceRun) {
	if s.stopped() {
		return
	}
	full := s.currentDiagnostics() != nil
	s.performanceMu.Lock()
	if s.performanceClosing || s.performanceCurrent != r {
		s.performanceMu.Unlock()
		return
	}
	u := s.performanceUpdateLocked(full)
	schedule := s.performancePending.Offer(u)
	s.performanceMu.Unlock()
	if !schedule || s.stopped() {
		return
	}
	// Keep the FIRST post's time while coalescing. Using the latest sample's
	// timestamp would hide a UI stall longer than the one-second sample period.
	queued := time.Now()
	post := s.performancePost
	if post == nil {
		post = fyne.Do
	}
	post(func() {
		u := s.performancePending.Take()
		if u == nil || s.stopped() {
			return
		}
		s.performanceMu.Lock()
		current := !s.performanceClosing && s.performanceCurrent == u.run
		s.performanceMu.Unlock()
		if !current {
			return
		}
		if u.run == r && u.recorder != nil && s.performanceActive.Load() == r {
			u.recorder.ObserveUIQueue(time.Since(queued))
		}
		s.applyPerformanceUpdate(u)
	})
}

const performanceTabTitle = "性能"

// performanceView is the 设置 → 性能 tab. It is rebuilt with the settings
// window and dropped when that closes; the recorder keeps running either way.
type performanceView struct {
	window                              fyne.Window
	status, top, details, exportLabel   *widget.Label
	verdict                             *widget.Label
	tiles                               [4]*canvas.Text
	start, stop, mark, folder, previous *widget.Button
	profiles                            *widget.Check
	detailToggle                        *widget.Button
	detailOpen                          bool
}

func performanceLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	return l
}

// openPerformance shows the settings window on the 性能 tab.
func (s *session) openPerformance() {
	if s.stopped() {
		return
	}
	s.openSettings()
	if s.settingsTabs == nil {
		return
	}
	for i, item := range s.settingsTabs.Items {
		if item.Text == performanceTabTitle {
			s.settingsTabs.SelectIndex(i)
		}
	}
}

var performanceTileTitles = [4]string{"整机 CPU", "计时器 CPU", "整机内存", "整机 GPU"}

// performanceControls builds the tab. Only a status line, four numbers and a
// short process list refresh every second; the long developer text is filled
// only while its section is open.
func (s *session) performanceControls(w fyne.Window) fyne.CanvasObject {
	v := &performanceView{window: w, status: performanceLabel(""), top: widget.NewLabel(""), details: performanceLabel(""),
		exportLabel: performanceLabel(""), verdict: performanceLabel("")}
	v.top.TextStyle.Monospace = true
	v.profiles = widget.NewCheck("附带开发者调试数据（堆 / 协程 / CPU 采样）", nil)
	profiles := true
	s.performanceMu.Lock()
	if r := s.performanceCurrent; r != nil {
		profiles = r.profiles
	}
	s.performanceMu.Unlock()
	v.profiles.SetChecked(profiles)
	v.start = widget.NewButton("开始监测", func() { s.startPerformance(v.profiles.Checked) })
	v.stop = widget.NewButton("停止并生成报告", func() { s.stopPerformance(true) })
	v.mark = widget.NewButton("标记卡顿", func() { s.markPerformanceStall() })
	v.folder = widget.NewButton("打开报告文件夹", s.openPerformanceFolder)
	v.previous = widget.NewButton("生成上次记录的报告", func() { s.exportPreviousPerformance() })
	v.start.Importance = widget.HighImportance
	tiles := make([]fyne.CanvasObject, 0, len(v.tiles))
	for i := range v.tiles {
		v.tiles[i] = canvas.NewText("—", clockLive)
		v.tiles[i].TextSize = 22
		v.tiles[i].TextStyle.Bold = true
		caption := canvas.NewText(performanceTileTitles[i], tagIdle)
		caption.TextSize = 12
		tiles = append(tiles, container.NewVBox(caption, v.tiles[i]))
	}
	v.details.Hide()
	v.detailToggle = widget.NewButton("显示开发者明细", func() {
		v.detailOpen = !v.detailOpen
		if v.detailOpen {
			v.detailToggle.SetText("收起开发者明细")
			v.details.Show()
			s.refreshPerformanceView() // Fill on demand.
		} else {
			v.detailToggle.SetText("显示开发者明细")
			v.details.Hide()
		}
	})
	header := container.NewVBox(
		container.NewGridWithColumns(3, v.start, v.stop, v.mark),
		v.status)
	body := container.NewVScroll(container.NewVBox(
		sectionCard("现在的占用", container.NewVBox(container.NewGridWithColumns(4, tiles...), v.verdict)),
		sectionCard("谁在占用资源", v.top),
		sectionCard("报告", container.NewVBox(
			performanceLabel("复现卡顿时点“标记卡顿”，结束后点“停止并生成报告”，把生成的 ZIP 发给开发者。关闭设置窗口不会停止监测，最长记录 6 小时。"),
			container.NewGridWithColumns(2, v.folder, v.previous), v.exportLabel, v.profiles)),
		v.detailToggle, v.details))
	s.performanceView = v
	s.refreshPerformanceView()
	return container.NewBorder(header, nil, nil, nil, body)
}

// refreshPerformanceView re-applies the current state (Fyne thread only).
func (s *session) refreshPerformanceView() {
	full := s.currentDiagnostics() != nil
	s.performanceMu.Lock()
	u := s.performanceUpdateLocked(full)
	s.performanceMu.Unlock()
	s.applyPerformanceUpdate(u)
}

func performanceButtonText(u *performanceUpdate) string {
	if u.run != nil {
		switch u.phase {
		case performanceStarting, performanceRunning:
			return "设置 · 监测中"
		case performanceStopping:
			return "设置 · 生成报告"
		}
	}
	return "设置"
}

// performanceTabVisible reports whether the user can currently see the tab;
// when not, the once-per-second text updates are skipped entirely.
func (s *session) performanceTabVisible() bool {
	if s.performanceView == nil || s.settingsTabs == nil {
		return false
	}
	selected := s.settingsTabs.Selected()
	return selected != nil && selected.Text == performanceTabTitle
}

func (s *session) applyPerformanceUpdate(u *performanceUpdate) {
	if s.stopped() {
		return
	}
	// The overlay's settings button doubles as the "recording" indicator.
	if b := s.performanceButton; b != nil {
		if text := performanceButtonText(u); b.Text != text {
			b.SetText(text)
		}
	}
	// A finished export is announced even when the settings window is closed:
	// the user asked for a file and must learn where it is.
	if u.exportID > s.performanceSeenExport {
		s.performanceSeenExport = u.exportID
		if u.exportPath != "" {
			parent := s.win
			if s.settings != nil {
				parent = s.settings
			}
			if s.performanceExported != nil {
				s.performanceExported(parent, u.exportPath)
			} else if parent != nil {
				exportSaved(parent, "性能报告已保存", u.exportPath)
			}
		}
	}
	v := s.performanceView
	if v == nil {
		return
	}
	idle := u.run == nil || u.phase == performanceStopped || u.phase == performanceFailed
	busy := u.exporting || u.marking || u.offline
	setPerformanceEnabled(v.start, idle && !busy)
	setPerformanceEnabled(v.stop, u.run != nil && (u.phase == performanceStarting || u.phase == performanceRunning))
	setPerformanceEnabled(v.mark, u.run != nil && u.phase == performanceRunning && u.status.Active && !u.marking)
	setPerformanceEnabled(v.previous, idle && !busy)
	if idle && v.profiles.Disabled() {
		v.profiles.Enable()
	} else if !idle && !v.profiles.Disabled() {
		v.profiles.Disable()
	}
	setPerformanceText(v.status, performanceStatusText(u, time.Now()))
	setPerformanceText(v.exportLabel, u.exportPath)
	if !s.performanceTabVisible() {
		return
	}
	values := performanceTileValues(u.status.Latest)
	for i, text := range values {
		if v.tiles[i].Text != text {
			v.tiles[i].Text = text
			v.tiles[i].Refresh()
		}
	}
	setPerformanceText(v.verdict, performanceVerdictText(u))
	setPerformanceText(v.top, performanceTopText(u.status.Latest))
	if v.detailOpen {
		setPerformanceText(v.details, performanceDetailText(u))
	}
}

func setPerformanceText(label *widget.Label, text string) {
	if label.Text != text {
		label.SetText(text)
	}
}

func setPerformanceEnabled(b *widget.Button, on bool) {
	if on && b.Disabled() {
		b.Enable()
	}
	if !on && !b.Disabled() {
		b.Disable()
	}
}

func performanceElapsed(d time.Duration) string {
	total := int(d.Round(time.Second) / time.Second)
	if total < 0 {
		total = 0
	}
	if total >= 3600 {
		return fmt.Sprintf("%d 小时 %02d 分 %02d 秒", total/3600, total%3600/60, total%60)
	}
	if total >= 60 {
		return fmt.Sprintf("%d 分 %02d 秒", total/60, total%60)
	}
	return fmt.Sprintf("%d 秒", total)
}

func performanceStatusText(u *performanceUpdate, now time.Time) string {
	state := "未开始监测"
	if u.run != nil {
		switch u.phase {
		case performanceStarting:
			state = "正在后台启动性能监测…"
		case performanceRunning:
			state = "监测中"
		case performanceStopping:
			state = "正在后台停止并保存…"
		case performanceStopped:
			state = "监测已停止"
		case performanceFailed:
			state = "启动失败，可重试"
		}
	}
	if !u.status.StartedAt.IsZero() {
		end := now
		if u.status.EndedAt != nil {
			end = *u.status.EndedAt
		}
		state += " · 已记录 " + performanceElapsed(end.Sub(u.status.StartedAt))
	}
	if u.exporting {
		state += "\n正在后台生成报告 ZIP…"
	}
	if u.offline {
		state += "\n正在生成上次记录的报告…"
	}
	if u.fullDiagnostics {
		state += "\n诊断录制同时开启，会增加占用（已计入本报告）。"
	}
	if u.status.StopReason != "" && u.status.StopReason != "user_stopped" {
		state += "\n停止原因：" + performanceStopReason(u.status.StopReason)
	}
	for _, text := range []string{u.status.LastError, u.errorText, u.notice, u.offlineText} {
		if text != "" {
			state += "\n" + text
		}
	}
	return state
}

func performanceStopReason(reason string) string {
	switch reason {
	case "user_stopped":
		return "手动停止 / 程序退出"
	case "sample_limit":
		return "已达到 6 小时记录上限"
	case "byte_limit":
		return "已达到数据大小上限"
	case "collector_failed":
		return "采样器异常"
	case "write_failed":
		return "写入磁盘失败"
	case "finalize_failed":
		return "保存调试数据或关闭采样器失败（已有数据仍可生成报告）"
	default:
		return reason
	}
}

func performancePercent(values map[string]float64, key string) string {
	if v, ok := values[key]; ok && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 {
		return fmt.Sprintf("%.0f%%", v)
	}
	return "—"
}

func performanceBytes(v float64) string {
	switch {
	case v >= 1<<30:
		return fmt.Sprintf("%.1f GB", v/(1<<30))
	case v >= 1<<20:
		return fmt.Sprintf("%.0f MB", v/(1<<20))
	}
	return fmt.Sprintf("%.0f KB", v/(1<<10))
}

func performanceTileValues(sample *diagnostics.PerformanceSample) [4]string {
	if sample == nil {
		return [4]string{"—", "—", "—", "—"}
	}
	return [4]string{performancePercent(sample.Values, "system_cpu_percent"), performancePercent(sample.Values, "tree_cpu_percent"),
		performancePercent(sample.Values, "system_memory_used_percent"), performancePercent(sample.Values, "system_gpu_busiest_engine_percent")}
}

// performanceVerdictText is the one-line live reading under the four numbers:
// the timer's own share, the emulator's, and a hint about who is responsible
// when the machine is busy.
func performanceVerdictText(u *performanceUpdate) string {
	sample := u.status.Latest
	if sample == nil {
		if u.run == nil {
			return "点“开始监测”后，这里显示整机、计时器和模拟器各占多少。"
		}
		return "等待首个样本…"
	}
	v := sample.Values
	var parts []string
	if memory, ok := v["tree_working_set_bytes"]; ok {
		parts = append(parts, "计时器内存 "+performanceBytes(memory))
	}
	if cpu, ok := v["emulator_cpu_percent"]; ok {
		text := fmt.Sprintf("模拟器 CPU %.0f%%", cpu)
		if memory, ok := v["emulator_working_set_bytes"]; ok {
			text += " / 内存 " + performanceBytes(memory)
		}
		parts = append(parts, text)
	}
	if a := sample.Application; a.LoopMaxMS != nil {
		parts = append(parts, fmt.Sprintf("本秒最慢一轮识别 %.0f ms", *a.LoopMaxMS))
	}
	text := strings.Join(parts, " · ")
	system, hasSystem := v["system_cpu_percent"]
	timer, hasTimer := v["tree_cpu_percent"]
	memory := v["system_memory_used_percent"]
	switch {
	case hasSystem && hasTimer && system >= 80 && timer < 15:
		text += "\n整机 CPU 很高，但计时器只占很少：卡顿主要不是计时器造成的，看下面谁占得多。"
	case hasTimer && timer >= 25:
		text += "\n计时器自身 CPU 占用偏高。"
	case memory >= 90:
		text += "\n整机内存快用满了，系统会变卡。"
	}
	if reason := sample.Unavailable["system_gpu_busiest_engine_percent"]; reason != "" {
		text += "\nGPU 数据暂无：" + reason
	}
	return strings.TrimSpace(text)
}

// performanceTopText lists the heaviest programs right now, merged by name.
func performanceTopText(sample *diagnostics.PerformanceSample) string {
	if sample == nil {
		return "开始监测后显示占用最高的程序。"
	}
	if len(sample.Top) == 0 {
		if reason := sample.Unavailable["top_processes"]; reason != "" {
			return "进程列表不可用：" + reason
		}
		return "暂无进程数据。"
	}
	type row struct {
		name             string
		cpu, memory, gpu float64
		hasCPU, hasGPU   bool
		count            int
	}
	merged := make(map[string]*row)
	var order []*row
	for _, p := range sample.Top {
		name := p.Name
		switch p.Role {
		case "self", "child":
			name = "计时器（本程序）"
		case "emulator":
			name += "（模拟器）"
		}
		r := merged[name]
		if r == nil {
			r = &row{name: name}
			merged[name] = r
			order = append(order, r)
		}
		r.count++
		r.memory += p.WorkingSetBytes
		if p.CPUPercent != nil {
			r.cpu, r.hasCPU = r.cpu+*p.CPUPercent, true
		}
		if p.GPUPercent != nil {
			r.gpu, r.hasGPU = math.Max(r.gpu, *p.GPUPercent), true
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].cpu != order[j].cpu {
			return order[i].cpu > order[j].cpu
		}
		return order[i].memory > order[j].memory
	})
	var lines []string
	for _, r := range order[:min(8, len(order))] {
		cpu, gpu := "  —  ", ""
		if r.hasCPU {
			cpu = fmt.Sprintf("%4.1f%%", r.cpu)
		}
		if r.hasGPU && r.gpu >= 0.5 {
			gpu = fmt.Sprintf("  GPU %.0f%%", r.gpu)
		}
		name := r.name
		if r.count > 1 {
			name += fmt.Sprintf(" ×%d", r.count)
		}
		lines = append(lines, fmt.Sprintf("CPU %s  内存 %8s%s  %s", cpu, performanceBytes(r.memory), gpu, name))
	}
	return strings.Join(lines, "\n")
}

func performanceMS(v *float64) string {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return "—"
	}
	return fmt.Sprintf("%.1f ms", *v)
}

func performanceOnOff(on bool) string {
	if on {
		return "开"
	}
	return "关"
}

func performanceMetricValue(values map[string]float64, key, unit string) string {
	v, ok := values[key]
	if !ok || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return "—"
	}
	switch unit {
	case "bytes":
		return performanceBytes(v)
	case "bytes/s":
		return performanceBytes(v) + "/秒"
	case "%":
		return fmt.Sprintf("%.1f%%", v)
	case "ms":
		return fmt.Sprintf("%.1f ms", v)
	case "cores":
		return fmt.Sprintf("%.2f 核", v)
	case "seconds":
		return fmt.Sprintf("%.1f 秒", v)
	case "count/s":
		return fmt.Sprintf("%.1f /秒", v)
	}
	return fmt.Sprintf("%.0f", v)
}

// performanceDetailText is the developer section: hardware, pipeline timing,
// every metric and the reasons for anything that could not be measured. All of
// it is also in the exported report; it is built only while the section is open.
func performanceDetailText(u *performanceUpdate) string {
	if u.recorder == nil {
		return "开始监测后显示硬件、识别链路耗时、全部指标和未测到数据的原因。"
	}
	m := u.metadata
	var b strings.Builder
	osVersion := m.System.OSVersion
	if osVersion == "" {
		osVersion = m.System.OS
	}
	fmt.Fprintf(&b, "版本 %s · %s · %s\nCPU：%s · %d 逻辑核 · 内存 %s\n", m.Version, osVersion, m.System.NativeArch,
		m.System.CPUName, m.System.LogicalCPUs, performanceBytes(float64(m.System.TotalRAMBytes)))
	for _, gpu := range m.System.GPUs {
		fmt.Fprintf(&b, "显卡：%s · 驱动 %s", gpu.Name, gpu.Driver)
		if gpu.DedicatedBytes > 0 {
			fmt.Fprintf(&b, " · 显存 %s", performanceBytes(float64(gpu.DedicatedBytes)))
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "采集 %s · 对局 %d ms / 待机 %d ms · 文字识别 %s · GPU 加速 %s\n", m.Settings.CaptureProvider, m.Settings.PollMS,
		m.Settings.IdlePollMS, performanceOnOff(m.Settings.OCREnabled), performanceOnOff(m.Settings.OCRGPURequested))
	sample := u.status.Latest
	if sample == nil {
		return b.String() + "\n等待首个样本…"
	}
	a := sample.Application
	fmt.Fprintf(&b, "\n识别链路（本秒）\n%s · %s · %d×%d\n最慢一轮 %s（截图 %s / 识别 %s / 状态更新与刷新 %s）\n采集到交付最长 %s · 界面线程最长等待 %s\n累计：交付 %d · 慢帧 %d · 错误 %d",
		a.Backend, a.Scene, a.Width, a.Height, performanceMS(a.LoopMaxMS), performanceMS(a.CaptureMaxMS), performanceMS(a.AnalysisMaxMS),
		performanceMS(a.PostMaxMS), performanceMS(a.AttemptMaxMS), performanceMS(a.UIQueueMaxMS), a.Attempts, a.SlowFrames, a.Errors)
	if a.LastError != "" {
		b.WriteString("\n最近错误：" + a.LastError)
	}
	b.WriteString("\n\n全部指标\n")
	for _, metric := range diagnostics.PerformanceMetrics {
		if _, ok := sample.Values[metric.Key]; ok {
			fmt.Fprintf(&b, "%s：%s\n", metric.Label, performanceMetricValue(sample.Values, metric.Key, metric.Unit))
		}
	}
	reasons := make(map[string]string)
	for key, reason := range m.System.Unavailable {
		reasons["系统 "+key] = reason
	}
	for key, reason := range sample.Unavailable {
		reasons[key] = reason
	}
	for key, status := range u.status.ProfileStatus {
		if status != "ok" {
			reasons["调试数据 "+key] = status
		}
	}
	if len(reasons) > 0 {
		b.WriteString("\n未测到的数据\n")
		keys := make([]string, 0, len(reasons))
		for key := range reasons {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "%s：%s\n", key, reasons[key])
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// performanceDirectory returns the newest session directory: the current run's
// if any, else the latest one on disk.
func (s *session) performanceDirectory() string {
	s.performanceMu.Lock()
	directory := ""
	if r := s.performanceCurrent; r != nil {
		directory = r.status.Directory
	}
	s.performanceMu.Unlock()
	if directory != "" {
		return directory
	}
	s.mu.Lock()
	root := s.cfg.Debug.Directory
	s.mu.Unlock()
	if sessions := diagnostics.PerformanceSessions(root); len(sessions) > 0 {
		return sessions[0]
	}
	return ""
}

func (s *session) openPerformanceFolder() {
	directory := s.performanceDirectory()
	if directory == "" {
		s.setPerformanceOffline(false, "还没有任何性能记录。")
		return
	}
	if err := openExportFolder(filepath.Join(directory, "performance-report.zip")); err != nil {
		s.setPerformanceOffline(false, "打开文件夹失败："+err.Error())
	}
}

// setPerformanceOffline publishes the state of the "previous session" export
// and refreshes the view through the normal coalesced path.
func (s *session) setPerformanceOffline(busy bool, text string) {
	s.performanceMu.Lock()
	s.performanceOffline, s.performanceOfflineText = busy, text
	s.performanceMu.Unlock()
	s.postPerformanceRefresh()
}

func (s *session) postPerformanceRefresh() {
	if s.stopped() {
		return
	}
	post := s.performancePost
	if post == nil {
		post = fyne.Do
	}
	post(func() {
		if !s.stopped() {
			s.refreshPerformanceView()
		}
	})
}

// exportPreviousPerformance turns the newest finished session on disk into a
// report: for "it lagged, I closed everything, now I want to send the report"
// and for sessions cut short by a crash.
func (s *session) exportPreviousPerformance() bool {
	s.mu.Lock()
	root := s.cfg.Debug.Directory
	s.mu.Unlock()
	s.performanceMu.Lock()
	r := s.performanceCurrent
	if s.performanceClosing || s.stopped() || s.performanceOffline ||
		(r != nil && r.phase != performanceStopped && r.phase != performanceFailed) {
		s.performanceMu.Unlock()
		return false
	}
	s.performanceOffline, s.performanceOfflineText = true, ""
	ctx, cancel := context.WithCancel(context.Background())
	s.performanceOfflineCancel = cancel
	s.performanceWorkers.Add(1)
	s.performanceMu.Unlock()
	s.postPerformanceRefresh()
	go func() {
		defer s.performanceWorkers.Done()
		defer cancel()
		sessions := diagnostics.PerformanceSessions(root)
		if len(sessions) == 0 {
			s.setPerformanceOffline(false, "还没有任何性能记录。")
			return
		}
		path, err := diagnostics.ExportPerformanceSession(ctx, sessions[0])
		if err != nil {
			s.setPerformanceOffline(false, "生成失败："+err.Error())
			return
		}
		s.performanceMu.Lock()
		s.performanceOffline, s.performanceOfflineText = false, "已生成："+path
		s.performanceExportID++
		s.performanceOfflineExport, s.performanceOfflinePath = s.performanceExportID, path
		s.performanceMu.Unlock()
		s.postPerformanceRefresh()
	}()
	return true
}
