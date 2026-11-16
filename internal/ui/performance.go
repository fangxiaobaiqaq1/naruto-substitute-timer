package ui

import (
	"context"
	"errors"
	"fmt"
	"math"
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

	// Fyne-thread-only objects. Closing the view does not stop the recorder.
	performanceWindow fyne.Window
	performanceView   *performanceView
	performanceButton *widget.Button

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
	s.mu.Unlock()
	s.performanceMu.Lock()
	old := s.performanceCurrent
	if s.performanceClosing || s.stopped() || (old != nil &&
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
	export := r.exportAfterStop
	s.performanceMu.Unlock()
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
		r.cancel() // cancel accepted exports, not just the visible window
		r.exportAfterStop = false
		r.stopOnce.Do(func() { close(r.stop) })
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
			r.notice = "已导出性能 ZIP；不会自动上传。"
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
		err := r.recorder.Mark("用户标记：卡顿") // fixed label, never account/free-form text
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

var performanceFrameError = errors.New("capture/analysis error")

// One atomic load when off. Never enqueue a Frame/image or nest the session,
// diagnostics and performance locks. The recorder consumes borrowed pixels only
// to read bounds synchronously; only timing/counter fields cross this boundary.
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
	received := time.Now()
	poll := s.poll()
	full := s.currentDiagnostics() != nil
	observation := frame.Frame{Img: f.Img, CaptureStarted: f.CaptureStarted, CapturedAt: f.CapturedAt,
		AnalysisStarted: f.AnalysisStarted, AnalyzedAt: f.AnalyzedAt, Sequence: f.Sequence,
		Hold: f.Hold, Duplicate: f.Duplicate, Fighting: f.Fighting, CaptureMethod: performanceBackend(f.CaptureMethod)}
	switch f.Scene {
	case "fight", "lobby", "result", "vs", "queue", "pick", "ban", "blank", "unsupported-resolution", "":
		observation.Scene = f.Scene
	default:
		observation.Scene = "unknown"
	}
	if f.Err != nil {
		observation.Err = performanceFrameError
	}
	r.recorder.ObserveFrame(observation, received, poll, full)
}

func performanceBackend(source string) string {
	source = strings.ToLower(source)
	switch {
	case strings.Contains(source, "printwindow"):
		return "printwindow-fullcontent"
	case strings.Contains(source, "mumu"):
		return "mumu-sdk"
	case strings.Contains(source, "leidian") || strings.Contains(source, "雷电"):
		if strings.Contains(source, "window") {
			return "leidian-window"
		}
		if strings.Contains(source, "raw") {
			return "leidian-adb-raw"
		}
		return "leidian-adb"
	default:
		return "unknown"
	}
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

type performanceView struct {
	status, hardware, cpu, memory, gpu, application, processes, metrics, availability, exportLabel *widget.Label
	start, stop, export, mark                                                                      *widget.Button
	profiles                                                                                       *widget.Check
	seenExport                                                                                     uint64
}

func performanceLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	return l
}

func (s *session) openPerformance() {
	if s.stopped() {
		return
	}
	if s.performanceWindow != nil {
		s.performanceWindow.Show()
		s.performanceWindow.RequestFocus()
		return
	}
	w := fyne.CurrentApp().NewWindow("性能报告 · CPU / 内存 / GPU")
	v := &performanceView{status: performanceLabel(""), hardware: performanceLabel(""),
		cpu: performanceLabel(""), memory: performanceLabel(""), gpu: performanceLabel(""),
		application: performanceLabel(""), processes: performanceLabel(""), metrics: performanceLabel(""),
		availability: performanceLabel(""), exportLabel: performanceLabel("")}
	v.profiles = widget.NewCheck("保存开始/结束 Go 堆与协程快照（可选）", nil)
	profiles := true
	s.performanceMu.Lock()
	v.seenExport = s.performanceExportID
	if r := s.performanceCurrent; r != nil {
		profiles = r.profiles
	}
	s.performanceMu.Unlock()
	v.profiles.SetChecked(profiles)
	v.start = widget.NewButton("开始性能监测", func() { s.startPerformance(v.profiles.Checked) })
	v.stop = widget.NewButton("停止并导出 ZIP", func() { s.stopPerformance(true) })
	v.export = widget.NewButton("导出当前快照 ZIP", func() { s.exportPerformance() })
	v.mark = widget.NewButton("标记卡顿", func() { s.markPerformanceStall() })
	v.start.Importance = widget.HighImportance
	s.performanceWindow, s.performanceView = w, v
	w.SetOnClosed(func() { s.performanceWindow, s.performanceView = nil, nil })
	addToggleMiniShortcut(w, s)
	content := container.NewVBox(
		performanceLabel("独立性能监测：默认关闭，仅本次运行。每秒采样 CPU、内存、GPU 和进程树；不启用全量诊断、不录制游戏图像、不采账号或 config 路径，也不自动上传。"),
		performanceLabel("关闭本窗口仍继续监测，主窗口按钮会标记进行中。最多 21600 个样本（1Hz 约 6 小时）/ 时间线 64 MiB，达到上限自动停止；退出程序会停止并保留原始数据。"),
		v.profiles, performanceLabel("快照包含分配统计、函数栈及源码文件名，非完整内存转储；分享前可取消勾选。快照与导出本身会产生额外开销。"),
		container.NewGridWithColumns(2, v.start, v.stop, v.export, v.mark), v.status,
		container.NewGridWithColumns(3, sectionCard("CPU", v.cpu), sectionCard("RAM / 内存", v.memory), sectionCard("GPU", v.gpu)),
		sectionCard("采集与 UI 调度", v.application), sectionCard("已观测进程树", v.processes),
		performanceLabel("CPU 百分比以全机逻辑核归一；工作集合计可能重复共享页。独立模拟器和预先存在的 ADB 服务不属于本进程树；GPU 为最忙引擎，缺失值不当作 0。"),
		sectionCard("系统与本次设置", v.hardware), sectionCard("指标明细", v.metrics),
		sectionCard("不可用原因 / 采集限制", v.availability), v.exportLabel)
	w.SetContent(container.NewStack(canvas.NewRectangle(panelBG), container.NewPadded(container.NewVScroll(content))))
	w.Resize(fyne.NewSize(880, 720))
	full := s.currentDiagnostics() != nil
	s.performanceMu.Lock()
	u := s.performanceUpdateLocked(full)
	s.performanceMu.Unlock()
	s.applyPerformanceUpdate(u)
	w.Show()
	w.RequestFocus()
}

func performanceButtonText(u *performanceUpdate) string {
	if u.run == nil {
		return "性能报告"
	}
	switch u.phase {
	case performanceStarting:
		return "性能报告 · 启动中"
	case performanceRunning:
		return "性能报告 · 进行中"
	case performanceStopping:
		return "性能报告 · 停止中"
	}
	return "性能报告"
}

func (s *session) applyPerformanceUpdate(u *performanceUpdate) {
	if s.stopped() {
		return
	}
	if b := s.performanceButton; b != nil {
		if text := performanceButtonText(u); b.Text != text {
			b.SetText(text)
		}
	}
	v := s.performanceView
	if v == nil {
		return
	}
	idle := u.run == nil || u.phase == performanceStopped || u.phase == performanceFailed
	setPerformanceEnabled(v.start, idle && !u.exporting && !u.marking)
	setPerformanceEnabled(v.stop, u.run != nil && (u.phase == performanceStarting || u.phase == performanceRunning))
	setPerformanceEnabled(v.export, u.recorder != nil && !u.exporting && (u.phase == performanceRunning || u.phase == performanceStopped))
	setPerformanceEnabled(v.mark, u.run != nil && u.phase == performanceRunning && u.status.Active && !u.marking)
	if idle && v.profiles.Disabled() {
		v.profiles.Enable()
	} else if !idle && !v.profiles.Disabled() {
		v.profiles.Disable()
	}
	setPerformanceText(v.status, performanceStatusText(u, time.Now()))
	setPerformanceText(v.cpu, performanceCard(u, [][2]string{{"system_cpu_percent", "系统"}, {"tree_cpu_percent", "进程树"}, {"tree_cpu_cores", "核等效"}}))
	setPerformanceText(v.memory, performanceCard(u, [][2]string{{"main_working_set_bytes", "主进程工作集"}, {"tree_working_set_bytes", "进程树工作集"}, {"tree_private_bytes", "私有提交"}, {"go_heap_alloc_bytes", "Go 堆"}}))
	setPerformanceText(v.gpu, performanceCard(u, [][2]string{{"system_gpu_busiest_engine_percent", "系统最忙引擎"}, {"main_gpu_busiest_engine_percent", "主进程最忙引擎"}, {"tree_gpu_busiest_engine_percent", "进程树最忙引擎"}, {"tree_gpu_dedicated_bytes", "专用显存"}, {"tree_gpu_shared_bytes", "共享内存"}}))
	setPerformanceText(v.hardware, performanceHardwareText(u))
	setPerformanceText(v.application, performanceApplicationText(u.status.Latest))
	setPerformanceText(v.processes, performanceProcessesText(u.status.Latest, u.metadata.RootPID))
	setPerformanceText(v.metrics, performanceMetricsText(u.status.Latest))
	setPerformanceText(v.availability, performanceAvailabilityText(u))
	setPerformanceText(v.exportLabel, u.exportPath)
	if u.exportID > v.seenExport {
		v.seenExport = u.exportID
		if u.exportPath != "" {
			if s.performanceExported != nil {
				s.performanceExported(s.performanceWindow, u.exportPath)
			} else {
				exportSaved(s.performanceWindow, "性能报告（ZIP，不含游戏图像）", u.exportPath)
			}
		}
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

func performanceStatusText(u *performanceUpdate, now time.Time) string {
	state := "性能监测关闭 · 尚未开始"
	if u.run != nil {
		switch u.phase {
		case performanceStarting:
			state = "正在后台启动性能监测…"
		case performanceRunning:
			state = "性能监测进行中"
		case performanceStopping:
			state = "正在后台停止 / 保存快照…"
		case performanceStopped:
			state = "性能监测已停止"
		case performanceFailed:
			state = "性能监测启动失败"
		}
	}
	if !u.status.StartedAt.IsZero() {
		end := now
		if u.status.EndedAt != nil {
			end = *u.status.EndedAt
		}
		duration := end.Sub(u.status.StartedAt).Round(time.Second)
		if duration < 0 {
			duration = 0
		}
		state += fmt.Sprintf(" · 时长 %s · %d 样本 · 时间线 %.2f MiB", duration, u.status.Samples, float64(u.status.Bytes)/(1<<20))
	}
	if u.exporting {
		state += "\n正在后台导出 ZIP…"
	}
	if u.fullDiagnostics {
		state += "\n全量诊断：并行开启，其资源开销也计入本报告。"
	} else {
		state += "\n全量诊断：关闭（与本开关独立）。"
	}
	if u.status.StopReason != "" {
		state += "\n停止原因：" + performanceStopReason(u.status.StopReason)
	}
	for _, text := range []string{u.status.LastError, u.errorText, u.notice} {
		if text != "" {
			state += "\n" + text
		}
	}
	return state
}

func performanceStopReason(reason string) string {
	switch reason {
	case "user_stopped":
		return "用户停止 / 应用退出"
	case "sample_limit":
		return "已达到 21600 个样本上限（1Hz 约 6 小时）"
	case "byte_limit":
		return "已达到时间线 64 MiB 上限"
	case "collector_failed":
		return "采样器异常"
	case "write_failed":
		return "写入失败"
	case "finalize_failed":
		return "保存快照/关闭采样器失败（仍可尝试导出已有数据）"
	default:
		return reason
	}
}

func performanceMetricValue(values map[string]float64, key string) string {
	if (key == "go_gogc_percent" || key == "go_gc_disabled") && values["go_gc_disabled"] == 1 {
		return "GC 已关闭"
	}
	if (key == "go_memory_limit_bytes" || key == "go_memory_limit_disabled") && values["go_memory_limit_disabled"] == 1 {
		return "未设置（无限制）"
	}
	v, ok := values[key]
	if !ok || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return "不可用"
	}
	switch {
	case strings.HasSuffix(key, "bytes_s"):
		return fmt.Sprintf("%.2f MiB/s", v/(1<<20))
	case strings.HasSuffix(key, "bytes"):
		return fmt.Sprintf("%.1f MiB", v/(1<<20))
	case strings.HasSuffix(key, "percent"):
		return fmt.Sprintf("%.1f%%", v)
	case strings.HasSuffix(key, "cores"):
		return fmt.Sprintf("%.2f 核", v)
	case strings.HasSuffix(key, "_ms"):
		return fmt.Sprintf("%.2f ms", v)
	case strings.HasSuffix(key, "_seconds"):
		return fmt.Sprintf("%.3f s", v)
	case strings.HasSuffix(key, "_s") || strings.HasSuffix(key, "_fps"):
		return fmt.Sprintf("%.2f /s", v)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

func performanceCard(u *performanceUpdate, fields [][2]string) string {
	if u.status.Latest == nil {
		return "不可用 · 等待首个样本"
	}
	var lines []string
	for _, f := range fields {
		value := performanceMetricValue(u.status.Latest.Values, f[0])
		reason := u.status.Latest.Unavailable[f[0]]
		if value == "不可用" {
			if reason == "" && strings.Contains(f[0], "gpu") {
				for _, key := range sortedPerformanceKeys(u.status.Latest.Unavailable) {
					if strings.Contains(strings.ToLower(key), "gpu") {
						reason = u.status.Latest.Unavailable[key]
						break
					}
				}
			}
			if reason == "" {
				reason = "本次未提供；见采集限制"
			}
		}
		// A partial tree sum is a value, but must not look like a complete sum.
		if reason != "" {
			value += "（" + reason + "）"
		}
		lines = append(lines, f[1]+"："+value)
	}
	return strings.Join(lines, "\n")
}

func performanceHardwareText(u *performanceUpdate) string {
	if u.recorder == nil {
		return "启动后显示系统、版本及非敏感设置；不读取或附加 config 文件。"
	}
	m := u.metadata
	cpu, ram := "不可用", "不可用"
	if m.System.LogicalCPUs > 0 {
		cpu = fmt.Sprintf("%d", m.System.LogicalCPUs)
	}
	if m.System.TotalRAMBytes > 0 {
		ram = fmt.Sprintf("%.1f GiB", float64(m.System.TotalRAMBytes)/(1<<30))
	}
	osVersion := m.System.OSVersion
	if osVersion == "" {
		osVersion = m.System.OS
	}
	text := fmt.Sprintf("版本 %s · %s · %s / %s\nCPU：%s · 逻辑核 %s · 物理内存 %s\n捕获 %s · 对局 %d ms / 待机 %d ms · 超时 %d ms\nOCR %t · VS OCR %t · 请求 DirectML %t（不代表实际后端） · %s 模式",
		m.Version, osVersion, m.System.NativeArch, m.System.ProcessArch, m.System.CPUName, cpu,
		ram, m.Settings.CaptureProvider, m.Settings.PollMS, m.Settings.IdlePollMS,
		m.Settings.CaptureTimeoutMS, m.Settings.OCREnabled, m.Settings.PreMatchOCR, m.Settings.OCRGPURequested, m.Settings.OverlayMode)
	text += "\nGo：" + m.GoVersion
	if revision := m.Build["vcs.revision"]; revision != "" {
		text += " · 构建 " + revision + " · 修改状态 " + m.Build["vcs.modified"]
	}
	for _, gpu := range m.System.GPUs {
		text += "\nGPU：" + gpu.Name + " · 驱动 " + gpu.Driver
	}
	if len(m.System.GPUs) == 0 {
		text += "\nGPU 硬件信息不可用（不代表没有 GPU）。"
	}
	return text
}

func performanceMS(v *float64) string {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return "不可用"
	}
	return fmt.Sprintf("%.2f ms", *v)
}

func performanceApplicationText(sample *diagnostics.PerformanceSample) string {
	if sample == nil {
		return "等待采样；UI 等待不是 GPU 呈现延迟，交付次数不是游戏 FPS。"
	}
	a := sample.Application
	geometry, poll := "尚无图像尺寸", "不可用"
	if a.Width > 0 && a.Height > 0 {
		geometry = fmt.Sprintf("%d×%d", a.Width, a.Height)
	}
	if a.PollMS > 0 {
		poll = fmt.Sprintf("%.0f ms", a.PollMS)
	}
	return fmt.Sprintf("样本 %s · 计数器采样/统计 %.2f ms（不含写盘/导出）\n%s · %s · %s · 轮询 %s · 样本中的全量诊断 %t\n交付 %d / 已分析 %d / 重复 %d / hold %d / 错误 %d / 超预算 %d（累计）\n采集 %s · 分析 %s · 交付年龄 %s\n窗口 P95：采集 %s / 分析 %s · UI 排队 %s\n标记：%s",
		sample.At.Format("15:04:05"), sample.CollectMS, a.Backend, a.Scene, geometry, poll, a.FullDiagnostics,
		a.Attempts, a.Analyzed, a.Duplicates, a.Held, a.Errors, a.SlowFrames,
		performanceMS(a.CaptureMS), performanceMS(a.AnalysisMS), performanceMS(a.DeliveryAgeMS),
		performanceMS(a.CaptureP95MS), performanceMS(a.AnalysisP95MS), performanceMS(a.UIQueueMS), strings.Join(sample.Markers, "；"))
}

func performanceProcessesText(sample *diagnostics.PerformanceSample, rootPID int) string {
	if sample == nil || len(sample.Processes) == 0 {
		return "进程明细不可用 / 等待采样（不当作 0 占用）。"
	}
	processes := append([]diagnostics.PerformanceProcess(nil), sample.Processes...)
	sort.Slice(processes, func(i, j int) bool { return processes[i].PID < processes[j].PID })
	var lines []string
	for _, p := range processes {
		role := "子进程"
		if p.PID == rootPID {
			role = "主进程"
		}
		lines = append(lines, fmt.Sprintf("%s %s · PID %d / 父 PID %d\nCPU %s · %s · 工作集 %s · 私有提交 %s\n线程 %s · 句柄 %s · GPU 最忙引擎 %s",
			role, p.Name, p.PID, p.ParentPID, performanceMetricValue(p.Values, "cpu_percent"), performanceMetricValue(p.Values, "cpu_cores"),
			performanceMetricValue(p.Values, "working_set_bytes"), performanceMetricValue(p.Values, "private_bytes"),
			performanceMetricValue(p.Values, "threads"), performanceMetricValue(p.Values, "handles"), performanceMetricValue(p.Values, "gpu_busiest_engine_percent")))
		for _, key := range sortedPerformanceKeys(p.Unavailable) {
			lines = append(lines, "  "+key+"："+p.Unavailable[key])
		}
	}
	return strings.Join(lines, "\n\n")
}

func performanceMetricsText(sample *diagnostics.PerformanceSample) string {
	if sample == nil {
		return "等待首个样本；首次 CPU / I/O 速率需要第二个计数器样本。"
	}
	var lines []string
	known := make(map[string]bool)
	for _, metric := range diagnostics.PerformanceMetrics {
		known[metric.Key] = true
		lines = append(lines, metric.Label+"："+performanceMetricValue(sample.Values, metric.Key))
	}
	var extra []string
	for key := range sample.Values {
		if !known[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	for _, key := range extra {
		lines = append(lines, key+"："+performanceMetricValue(sample.Values, key))
	}
	return strings.Join(lines, "\n")
}

func sortedPerformanceKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func performanceAvailabilityText(u *performanceUpdate) string {
	var lines []string
	for _, key := range sortedPerformanceKeys(u.metadata.System.Unavailable) {
		lines = append(lines, "系统 "+key+"："+u.metadata.System.Unavailable[key])
	}
	if sample := u.status.Latest; sample != nil {
		for _, key := range sortedPerformanceKeys(sample.Unavailable) {
			lines = append(lines, key+"："+sample.Unavailable[key])
		}
	}
	for _, key := range sortedPerformanceKeys(u.status.ProfileStatus) {
		lines = append(lines, "快照 "+key+"："+u.status.ProfileStatus[key])
	}
	if len(lines) == 0 {
		return "尚无额外原因；缺失指标仅表示本次不可用，不代表占用为零。"
	}
	return strings.Join(lines, "\n")
}
