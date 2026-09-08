package ui

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/diagnostics"
)

// Recording belongs to this run, not saved settings: restarting must not
// silently resume saving game pixels. All raw images stay in the session folder.
type diagnosticsState struct {
	diagnosticMu       sync.Mutex
	diagnostic         *diagnostics.Recorder
	diagnosticLast     *diagnostics.Recorder
	diagnosticRecord   bool
	diagnosticError    string
	diagnosticWriters  sync.WaitGroup
	diagnosticLabel    *widget.Label // accessed on the Fyne thread
	diagnosticWindow   fyne.Window
	drawTraceAvailable bool

	// Protected by session.mu. Each component acknowledges the exact frame it
	// applied; queued and superseded work cannot acknowledge a newer frame.
	traceFrame   traceRef
	traceOverlay traceRef
	traceClock   traceRef
}

type traceRef struct {
	recorder *diagnostics.Recorder
	id       uint64
}

func (r traceRef) mark(stage string, at time.Time) {
	if r.recorder != nil && r.id != 0 {
		r.recorder.MarkUI([]uint64{r.id}, stage, at)
	}
}

func (s *session) currentDiagnostics() *diagnostics.Recorder {
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	return s.diagnostic
}

func (s *session) startDiagnostics(record bool) error {
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	if s.diagnostic != nil {
		return fmt.Errorf("诊断已经开启，请先停止当前会话")
	}
	if s.stopped() {
		return fmt.Errorf("计时器已关闭")
	}
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	root := cfg.Debug.Directory
	if root == "" {
		root = "debug"
	}
	r, err := diagnostics.New(diagnostics.Options{
		Root: filepath.Join(root, "sessions"), RecordFrames: record, RecordHUDEvidence: record, Config: cfg,
	})
	if err != nil {
		return err
	}
	s.diagnostic, s.diagnosticLast, s.diagnosticError = r, r, ""
	s.diagnosticRecord = record
	if source, ok := s.win.(frameDrawSource); ok {
		source.SetFrameDrawCallback(s.traceDrawn)
	}
	return nil
}

// Detach immediately, drain on a writer goroutine, and join it before Run exits.
// A slow PNG encoder must not hold up the Fyne event loop or the capture worker.
func (s *session) stopDiagnostics() {
	s.diagnosticMu.Lock()
	r := s.diagnostic
	s.diagnostic = nil
	if source, ok := s.win.(frameDrawSource); ok {
		source.SetFrameDrawCallback(nil)
	}
	if r != nil {
		s.diagnosticWriters.Add(1)
	}
	s.diagnosticMu.Unlock()
	if r == nil {
		return
	}
	go func() {
		defer s.diagnosticWriters.Done()
		if err := r.Close(); err != nil {
			s.diagnosticMu.Lock()
			s.diagnosticError = err.Error()
			s.diagnosticMu.Unlock()
		}
	}()
}

// Called while the model lock is held, immediately after SideClock.Observe.
// LastEvent carries first evidence time, not the time its confirmation returned.
func (s *session) traceConfirmed(ref traceRef, beforeLeft, beforeRight uint64, at time.Time) {
	if ref.recorder == nil || ref.id == 0 {
		return
	}
	leftAt, leftSerial := s.left.LastEvent()
	rightAt, rightSerial := s.right.LastEvent()
	if leftSerial != beforeLeft {
		ref.recorder.Confirm(diagnostics.Event{FrameID: ref.id, Side: "left", Serial: leftSerial,
			Number: s.left.EventCount(), FirstObservedAt: leftAt, ConfirmedAt: at, UIVisible: s.side != "left"})
	}
	if rightSerial != beforeRight {
		ref.recorder.Confirm(diagnostics.Event{FrameID: ref.id, Side: "right", Serial: rightSerial,
			Number: s.right.EventCount(), FirstObservedAt: rightAt, ConfirmedAt: at, UIVisible: s.side != "right"})
	}
}

// Caller holds session.mu while preparing the exact event labels to display.
// A serial survives Reset, whereas a zero per-match count means no badge for it.
func (s *session) visibleEventSerials() (left, right uint64) {
	if s.side != "left" && s.left.EventCount() > 0 {
		_, left = s.left.LastEvent()
	}
	if s.side != "right" && s.right.EventCount() > 0 {
		_, right = s.right.LastEvent()
	}
	return
}

// Fyne v2.6 has no public post-paint callback. build.bat installs a small,
// version-checked Go build overlay that adds this optional window capability.
// Plain go build and test drivers remain supported, with E2E unavailable.
type frameDrawSource interface {
	SetFrameDrawCallback(func(started, completed time.Time))
}

func (s *session) installDrawTrace() {
	source, ok := s.win.(frameDrawSource)
	s.drawTraceAvailable = ok
	if ok && s.currentDiagnostics() != nil {
		source.SetFrameDrawCallback(s.traceDrawn)
	}
}

func (s *session) traceApplied(ref traceRef, overlay bool) {
	s.mu.Lock()
	if overlay {
		s.traceOverlay = ref
	} else {
		s.traceClock = ref
	}
	ready := ref.id != 0 && ref == s.traceOverlay && ref == s.traceClock
	s.mu.Unlock()
	if ready {
		ref.mark("applied", time.Now())
		// Even an unchanged text value needs a real draw to get a draw sample.
		// This only runs while diagnostics are enabled and is recording overhead.
		if s.info != nil {
			s.info.Refresh()
		}
	}
}

// This runs after the visible window's native buffer swap, on the same Fyne
// thread as both UI updates. No UI callback can interleave with this paint.
func (s *session) traceDrawn(started, completed time.Time) {
	s.mu.Lock()
	ref := s.traceOverlay
	ready := ref.id != 0 && ref == s.traceClock
	s.mu.Unlock()
	if ready {
		ref.mark("drawing", started)
		ref.mark("drawn", completed)
	}
}

func (s *session) diagnosticText() string {
	s.diagnosticMu.Lock()
	r, active, lastError := s.diagnosticLast, s.diagnostic != nil, s.diagnosticError
	s.diagnosticMu.Unlock()
	state := "诊断关闭 · 仅本次开启，不随启动保存"
	if r != nil {
		v := r.Snapshot()
		state = "已停止（后台正在保存）"
		if active {
			state = "诊断进行中"
		} else if v.Finalized {
			state = "诊断已停止"
		}
		state += fmt.Sprintf("\n采集 %d · 有效 %d · 绘制样本 %d · 事件样本 %d\n原帧已存 %d · 场景跳过 %d · 丢帧 %d · 丢日志 %d\n%s",
			v.Attempts, v.ValidFrames, v.CompleteFrames, v.CompleteEvents, v.RecordedFrames, v.SkippedFrames, v.DroppedFrames, v.DroppedLogs, v.Directory)
		state += fmt.Sprintf("\nHUD 证据 %d · HUD 丢弃 %d · 已用 %.1f MiB / 1024 MiB", v.HUDFrames, v.DroppedHUD, float64(v.RecordingBytes)/(1<<20))
		if v.LastError != "" {
			state += "\n写入异常：" + v.LastError
		}
	}
	if !s.drawTraceAvailable {
		state += "\n当前构建未接入绘制回调，端到端指标不可用。请用 build.bat 构建。"
	}
	if lastError != "" {
		state += "\n" + lastError
	}
	return state
}

func (s *session) refreshDiagnosticLabel() {
	if s.diagnosticLabel != nil {
		text := s.diagnosticText()
		if text != s.diagnosticLabel.Text {
			s.diagnosticLabel.SetText(text)
		}
	}
}

// Keep the controls in a dedicated window reachable without scrolling through
// unrelated settings. Reopening focuses the same controls and recording state.
func (s *session) openDiagnostics() {
	if s.stopped() {
		return
	}
	w := s.diagnosticWindow
	if w == nil {
		w = fyne.CurrentApp().NewWindow("采集与延迟诊断 · 原帧录制")
		s.diagnosticWindow = w
		w.SetOnClosed(func() { s.diagnosticWindow = nil; s.diagnosticLabel = nil })
		controls := s.diagnosticsControls(w)
		w.SetContent(container.NewStack(canvas.NewRectangle(panelBG), container.NewPadded(container.NewVScroll(controls))))
		w.Resize(fyne.NewSize(450, 480))
	}
	s.refreshDiagnosticLabel()
	w.Show()
	w.RequestFocus()
}

func (s *session) diagnosticsControls(w fyne.Window) fyne.CanvasObject {
	s.diagnosticLabel = widget.NewLabel(s.diagnosticText())
	s.diagnosticLabel.Wrapping = fyne.TextWrapWord
	record := widget.NewCheck("仅在对局中录制原帧（无损 PNG）", nil)
	s.diagnosticMu.Lock()
	record.SetChecked(s.diagnosticRecord)
	s.diagnosticMu.Unlock()
	if s.currentDiagnostics() != nil {
		record.Disable()
	}
	var start *widget.Button
	start = widget.NewButton("开始诊断", func() {
		if s.currentDiagnostics() != nil {
			s.stopDiagnostics()
			start.SetText("开始诊断")
			record.Enable()
		} else {
			if err := s.startDiagnostics(record.Checked); err != nil {
				dialog.ShowError(err, w)
				return
			}
			start.SetText("停止并生成报告")
			record.Disable()
		}
		s.refreshDiagnosticLabel()
	})
	if s.currentDiagnostics() != nil {
		start.SetText("停止并生成报告")
	}
	export := widget.NewButton("导出当前报告", func() {
		s.diagnosticMu.Lock()
		r := s.diagnosticLast
		if s.stopped() {
			s.diagnosticMu.Unlock()
			return
		}
		if r != nil {
			s.diagnosticWriters.Add(1)
		}
		s.diagnosticMu.Unlock()
		if r == nil {
			dialog.ShowInformation("诊断报告", "请先开始一次诊断。", w)
			return
		}
		go func() {
			defer s.diagnosticWriters.Done()
			path, err := r.ExportReport()
			if s.stopped() {
				return
			}
			fyne.Do(func() {
				if s.stopped() || s.diagnosticWindow != w {
					return
				}
				if err != nil {
					dialog.ShowError(err, w)
				} else {
					dialog.ShowInformation("诊断报告", "已保存："+path, w)
				}
				s.refreshDiagnosticLabel()
			})
		}()
	})
	open := widget.NewButton("打开日志目录", func() {
		s.diagnosticMu.Lock()
		r := s.diagnosticLast
		s.diagnosticMu.Unlock()
		if r == nil {
			return
		}
		path, err := filepath.Abs(r.Snapshot().Directory)
		if err == nil {
			u := &url.URL{Scheme: "file", Path: "/" + strings.ReplaceAll(path, "\\", "/")}
			err = fyne.CurrentApp().OpenURL(u)
		}
		if err != nil {
			dialog.ShowError(err, w)
		}
	})
	hint := widget.NewLabel("每次采集配对识别与事件日志。仅当前帧成功识别为对局且未保持旧结果时保存画面，进场自动开始、离场自动暂停；训练场识别为对局时也可录制。大厅、匹配、选人、结算和未知状态只写有限量的轻量日志。总录制上限仍为 1 GiB：768 MiB 保存无缩放、无标注的完整原帧，另预留 256 MiB 每秒最多保存两张名字与豆区域的原像素截图。完整原帧存满后 HUD 证据可继续保存；HUD 截图不作为完整帧回放。队列或额度满会标记丢弃。关闭此窗口不会停止诊断，请点击停止按钮结束。端到端截至原生缓冲提交，不包含游戏内部或显示器延迟。")
	hint.Wrapping = fyne.TextWrapWord
	return container.NewVBox(widget.NewLabel("采集与延迟诊断"), record, start,
		container.NewGridWithColumns(2, export, open), s.diagnosticLabel, hint)
}
