package ui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/buildinfo"
	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/diagnostics"
	"narutotimer/internal/hudtext"
	"narutotimer/internal/ocr"
	"narutotimer/internal/support"
)

// Recording belongs to this run, not saved settings: restarting must not
// silently resume saving game pixels. All raw images stay in the session folder.
type diagnosticsState struct {
	diagnosticMu       sync.Mutex
	diagnostic         *diagnostics.Recorder
	diagnosticLast     *diagnostics.Recorder
	diagnosticRecord   bool
	diagnosticReplay   bool
	diagnosticError    string
	diagnosticProbe    *mumu.ProbeResult
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

func (s *session) startDiagnostics(record bool, replayEnabled ...bool) error {
	replay := len(replayEnabled) > 0 && replayEnabled[0]
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
		Root: filepath.Join(root, "sessions"), RecordFrames: record, RecordHUDEvidence: record, Replay: replay, Config: cfg,
	})
	if err != nil {
		return err
	}
	s.diagnostic, s.diagnosticLast, s.diagnosticError = r, r, ""
	s.diagnosticProbe = nil
	s.diagnosticRecord = record
	s.diagnosticReplay = replay
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
	applied := time.Now()
	s.mu.Lock()
	if overlay {
		s.traceOverlay = ref
	} else {
		s.traceClock = ref
	}
	ready := ref.id != 0 && ref == s.traceOverlay && ref == s.traceClock
	s.mu.Unlock()
	if ref.recorder != nil && ref.id != 0 {
		ref.recorder.MarkReplayUIDrawn(ref.id, applied, time.Time{})
	}
	if ready {
		ref.mark("applied", applied)
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
		ref.recorder.MarkReplayUIDrawn(ref.id, time.Time{}, completed)
	}
}

func (s *session) exportSupportBundle(ctx context.Context, sessionDir string, probe *mumu.ProbeResult, includeImages bool) (string, error) {
	s.mu.Lock()
	cfg := s.cfg
	root := s.supportRoot
	cfgPath := s.cfgPath
	executable := s.executablePath
	s.mu.Unlock()
	if root == "" {
		root = cfg.Debug.Directory
	}
	inventory := mumu.DiscoverInventory(ctx, cfg.Capture.MuMu.InstallDir)
	return support.ExportBundle(ctx, support.BundleOptions{
		Root:          root,
		SessionDir:    sessionDir,
		Config:        cfg,
		ConfigPath:    cfgPath,
		CaptureState:  s.snapshotCaptureState(),
		Version:       buildinfo.Version,
		Executable:    executable,
		Inventory:     inventory,
		Probe:         probe,
		IncludeImages: includeImages,
	})
}

func (s *session) diagnosticText() string {
	s.diagnosticMu.Lock()
	r, active, lastError := s.diagnosticLast, s.diagnostic != nil, s.diagnosticError
	s.diagnosticMu.Unlock()
	s.mu.Lock()
	captureLost, captureStatus := s.captureLost, s.status
	textStatus, textError := s.textStatus, s.textError
	s.mu.Unlock()
	state := "诊断关闭 · 仅本次开启，不随启动保存"
	if r != nil {
		v := r.Snapshot()
		state = "已停止（后台正在保存）"
		if active {
			state = "诊断进行中"
		} else if v.Finalized {
			state = "诊断已停止"
		}
		state += fmt.Sprintf("\n采集 %d · 有效 %d · 绘制样本 %d · 事件样本 %d\n无损 PNG 原帧 %d · 场景跳过 %d · PNG 丢帧 %d · 丢日志 %d\n%s",
			v.Attempts, v.ValidFrames, v.CompleteFrames, v.CompleteEvents, v.RecordedFrames, v.SkippedFrames, v.DroppedFrames, v.DroppedLogs, v.Directory)
		state += fmt.Sprintf("\nPNG+HUD 占用 %.1f MiB / 1024 MiB（HUD %d 张，%.1f MiB，丢弃 %d）", float64(v.RecordingBytes)/(1<<20), v.HUDFrames, float64(v.HUDBytes)/(1<<20), v.DroppedHUD)
		state += fmt.Sprintf("\n回放 JPEG 独立占用 %.1f MiB / 64 MiB · 已保存 %d · 丢弃 %d · 淘汰 %d", float64(v.ReplayBytes)/(1<<20), v.ReplaySaved, v.ReplayDropped, v.ReplayEvicted)
		if v.LastError != "" {
			state += "\n写入异常：" + v.LastError
		}
	}
	if captureLost {
		state += "\n采集异常：" + captureStatus
	}
	library := hudtext.BundledLibraryInfo()
	state += fmt.Sprintf("\n版本 %s\n%s\n内置忍者库：%d 条资料 · %d 个名称\n文字识别状态：%s", buildinfo.Version, ocr.BackendName(), library.Records, library.Names, textStatus)
	if textError != "" {
		state += "\n文字识别异常：" + textError
	}
	if !s.drawTraceAvailable {
		state += "\n当前构建未接入绘制回调，端到端指标不可用。请用 build.bat 构建。"
	}
	if lastError != "" {
		state += "\n" + lastError
	}
	return state
}

func openExportFolder(path string) error {
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return err
	}
	return fyne.CurrentApp().OpenURL(&url.URL{Scheme: "file", Path: "/" + strings.ReplaceAll(dir, "\\", "/")})
}

func exportSaved(w fyne.Window, title, path string) {
	message := "已保存：" + path
	dialog.ShowCustomConfirm(title, "打开文件夹", "关闭", widget.NewLabel(message), func(open bool) {
		if open {
			if err := openExportFolder(path); err != nil {
				dialog.ShowError(err, w)
			}
		}
	}, w)
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
	record := widget.NewCheck("录制原始对局帧（无损 PNG，可能快速占用约 1GiB）", nil)
	replay := widget.NewCheck("保留停止前 3 分钟回放（独立 1fps JPEG，最多 64MiB）", nil)
	includeImages := widget.NewCheck("导出 ZIP 时包含原帧/回放图像（隐私选项）", nil)
	s.diagnosticMu.Lock()
	record.SetChecked(s.diagnosticRecord)
	replay.SetChecked(s.diagnosticReplay)
	s.diagnosticMu.Unlock()
	if s.currentDiagnostics() != nil {
		record.Disable()
		replay.Disable()
	}
	var start *widget.Button
	start = widget.NewButton("开始诊断", func() {
		if s.currentDiagnostics() != nil {
			s.stopDiagnostics()
			start.SetText("开始诊断")
			record.Enable()
			replay.Enable()
		} else {
			if err := s.startDiagnostics(record.Checked, replay.Checked); err != nil {
				dialog.ShowError(err, w)
				return
			}
			start.SetText("停止并生成报告")
			record.Disable()
			replay.Disable()
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
					exportSaved(w, "诊断报告", path)
				}
				s.refreshDiagnosticLabel()
			})
		}()
	})
	var exportReplay *widget.Button
	exportReplay = widget.NewButton("导出诊断回放（小型 ZIP）", func() {
		s.diagnosticMu.Lock()
		r := s.diagnosticLast
		s.diagnosticMu.Unlock()
		if r == nil {
			dialog.ShowInformation("诊断回放", "本次尚未开始诊断。请勾选回放并停止诊断后导出。", w)
			return
		}
		exportReplay.Disable()
		go func() {
			path, err := r.ExportReplay("")
			fyne.Do(func() {
				if s.stopped() || s.diagnosticWindow != w {
					return
				}
				exportReplay.Enable()
				if err != nil {
					dialog.ShowInformation("诊断回放", err.Error(), w)
					return
				}
				exportSaved(w, "诊断回放", path)
			})
		}()
	})
	var probe *widget.Button
	probe = widget.NewButton("验证 MuMu SDK", func() {
		s.mu.Lock()
		target := s.cfg.Capture.MuMu
		executable := s.executablePath
		s.mu.Unlock()
		if executable == "" {
			executable, _ = os.Executable()
		}
		probe.Disable()
		s.diagnosticLabel.SetText("正在通过独立进程验证 MuMu SDK…")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), mumu.HelperTransactionTimeout)
			defer cancel()
			result, err := support.ProbeSDK(ctx, executable, mumu.Options{
				InstallDir: target.InstallDir, DLLPath: target.DLLPath, Instance: target.Instance,
				DisplayID: target.DisplayID, Package: target.Package, Manual: target.Selection == "manual" || (target.Selection != "auto" && (target.InstallDir != "" || target.Instance != 0 || target.DLLPath != "")),
			})
			fyne.Do(func() {
				if s.stopped() || s.diagnosticWindow != w {
					return
				}
				probe.Enable()
				if err == nil {
					s.diagnosticMu.Lock()
					copy := result
					s.diagnosticProbe = &copy
					s.diagnosticMu.Unlock()
				}
				if err != nil {
					dialog.ShowError(err, w)
					return
				}
				if result.Error != "" {
					dialog.ShowInformation("MuMu SDK 自检", "失败阶段："+result.FailureStage+"\n"+result.Error, w)
					return
				}
				dialog.ShowInformation("MuMu SDK 自检", fmt.Sprintf("连接成功，已读取 %d × %d 图像。", result.Width, result.Height), w)
			})
		}()
	})
	var exportBundle *widget.Button
	exportBundle = widget.NewButton("导出支持诊断包（ZIP）", func() {
		s.diagnosticMu.Lock()
		r := s.diagnosticLast
		probeResult := s.diagnosticProbe
		include := includeImages.Checked
		if probeResult != nil {
			copy := *probeResult
			probeResult = &copy
		}
		if r != nil {
			s.diagnosticWriters.Add(1)
		}
		s.diagnosticMu.Unlock()
		sessionDir := ""
		if r != nil {
			sessionDir = r.Snapshot().Directory
		}
		exportBundle.Disable()
		go func() {
			if r != nil {
				defer s.diagnosticWriters.Done()
				_, _ = r.ExportReport()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			if probeResult == nil {
				s.mu.Lock()
				target := s.cfg.Capture.MuMu
				executable := s.executablePath
				s.mu.Unlock()
				if executable == "" {
					executable, _ = os.Executable()
				}
				probeCtx, stopProbe := context.WithTimeout(ctx, mumu.HelperTransactionTimeout)
				result, probeErr := support.ProbeSDK(probeCtx, executable, mumu.Options{
					InstallDir: target.InstallDir, DLLPath: target.DLLPath, Instance: target.Instance,
					DisplayID: target.DisplayID, Package: target.Package, Manual: target.Selection == "manual" || (target.Selection != "auto" && (target.InstallDir != "" || target.Instance != 0 || target.DLLPath != "")),
				})
				stopProbe()
				if probeErr != nil {
					result = mumu.ProbeResult{Requested: mumu.Options{InstallDir: target.InstallDir, DLLPath: target.DLLPath, Instance: target.Instance, DisplayID: target.DisplayID, Package: target.Package, Manual: target.Selection == "manual" || (target.Selection != "auto" && (target.InstallDir != "" || target.Instance != 0 || target.DLLPath != ""))}, FailureStage: "SDK 自检子进程", Error: probeErr.Error()}
				}
				probeResult = &result
			}
			path, err := s.exportSupportBundle(ctx, sessionDir, probeResult, include)
			fyne.Do(func() {
				if s.stopped() || s.diagnosticWindow != w {
					return
				}
				exportBundle.Enable()
				if err != nil {
					dialog.ShowError(err, w)
					return
				}
				exportSaved(w, "支持诊断包", path)
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
	openOCR := widget.NewButton("打开 OCR 启动日志", func() {
		path := ocr.RuntimeLogDirectory()
		if path == "" {
			dialog.ShowError(fmt.Errorf("无法获取用户日志目录"), w)
			return
		}
		err := os.MkdirAll(path, 0700)
		if err == nil {
			err = fyne.CurrentApp().OpenURL(&url.URL{Scheme: "file", Path: "/" + strings.ReplaceAll(path, "\\", "/")})
		}
		if err != nil {
			dialog.ShowError(err, w)
		}
	})
	hint := widget.NewLabel("无损 PNG 原帧与回放是两项独立开关：回放每秒最多一帧，保存原始游戏 JPEG 和同帧的 annotated JPEG（720px/质量75，两者共用64MiB上限）。标注图固定记录对应 frame_id 的识别、豆位、计时器显示和绘制时刻；没有同帧 UI 状态会明确写不可用，不会借用新帧。导出后可直接打开所在文件夹；支持包默认不含图像。")
	hint.Wrapping = fyne.TextWrapWord
	return container.NewVBox(widget.NewLabel("采集与延迟诊断"), record, replay, includeImages, start,
		container.NewGridWithColumns(2, export, exportReplay),
		container.NewGridWithColumns(2, exportBundle, probe),
		container.NewGridWithColumns(2, open, openOCR), s.diagnosticLabel, hint)
}
