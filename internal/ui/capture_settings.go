package ui

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"narutotimer/internal/support"
)

func WithCaptureSelection(selectTarget func(config.MuMuCaptureConfig) uint64) Option {
	return func(s *session) { s.captureControl = selectTarget }
}
func WithInitialAbout(show bool) Option { return func(s *session) { s.initialAbout = show } }

func captureTargetText(target config.MuMuCaptureConfig) string {
	if target.Selection == "auto" || (target.Selection == "" && target.InstallDir == "" && target.Instance == 0 && target.DLLPath == "") {
		return "自动检测"
	}
	root := strings.TrimSpace(target.InstallDir)
	if root == "" {
		root = "未指定安装目录"
	}
	return fmt.Sprintf("%s · 实例 %d", root, target.Instance)
}

func selectionFromFields(root, instance string, base config.MuMuCaptureConfig) (config.MuMuCaptureConfig, error) {
	id, err := strconv.Atoi(strings.TrimSpace(instance))
	if err != nil || id < 0 {
		return config.MuMuCaptureConfig{}, fmt.Errorf("实例编号需要填写非负整数")
	}
	base.Selection = "manual"
	base.InstallDir = strings.TrimSpace(root)
	base.Instance = id
	base.DLLPath = ""
	if base.InstallDir == "" {
		return config.MuMuCaptureConfig{}, fmt.Errorf("请先选择 MuMu 安装目录")
	}
	return base, nil
}

// captureProbeSelection resolves the target for a non-persisting SDK probe.
// Selecting a row is preferred; when the list has exactly one candidate, an
// automatic draft can be tested without forcing the user to save a target.
// Multiple candidates remain an explicit-choice case so a probe never tests a
// different MuMu installation than the one the user intended.
func captureProbeSelection(selected int, items []mumu.ProcessChoice, root, instance string, draft config.MuMuCaptureConfig) (config.MuMuCaptureConfig, error) {
	if selected > 0 {
		if selected > len(items) {
			return config.MuMuCaptureConfig{}, fmt.Errorf("所选 MuMu 实例已过期，请重新扫描")
		}
		item := items[selected-1]
		draft.Selection = "manual"
		draft.InstallDir = item.Root
		draft.Instance = item.Index
		draft.DLLPath = ""
		return draft, nil
	}
	// The root/instance text fields may still contain the last manual target
	// after the user selected the automatic row. Do not let that stale text
	// override an explicitly selected automatic probe.
	if draft.Selection != "auto" && strings.TrimSpace(root) != "" {
		return selectionFromFields(root, instance, draft)
	}
	if draft.Selection == "manual" && strings.TrimSpace(draft.InstallDir) != "" {
		return draft, nil
	}
	if len(items) == 1 {
		item := items[0]
		draft.Selection = "manual"
		draft.InstallDir = item.Root
		draft.Instance = item.Index
		draft.DLLPath = ""
		return draft, nil
	}
	if len(items) > 1 {
		return config.MuMuCaptureConfig{}, fmt.Errorf("发现 %d 个 MuMu 实例，请先在列表中选择要测试的安装目录和实例编号", len(items))
	}
	return config.MuMuCaptureConfig{}, fmt.Errorf("没有可测试的 MuMu 实例，请先扫描，或填写安装目录和实例编号")
}

func (s *session) snapshotCaptureState() frame.CaptureState {
	if s.captureState != nil {
		return s.captureState()
	}
	s.mu.Lock()
	target := s.cfg.Capture.MuMu
	source := s.captureSource
	s.mu.Unlock()
	return frame.CaptureState{Requested: target, Applied: target, Source: source}
}

func captureStateText(path string, saved config.MuMuCaptureConfig, state frame.CaptureState) string {
	var lines []string
	lines = append(lines, "配置文件："+path)
	lines = append(lines, "已保存："+captureTargetText(saved))
	if state.RequestedRevision > 0 {
		lines = append(lines, fmt.Sprintf("已请求应用：%s（修订 %d）", captureTargetText(state.Requested), state.RequestedRevision))
		lines = append(lines, fmt.Sprintf("采集器已应用：%s（修订 %d）", captureTargetText(state.Applied), state.AppliedRevision))
		if state.RequestedRevision != state.AppliedRevision {
			lines = append(lines, "状态：正在切换，等待下一次采集请求完成")
		}
	}
	if state.Source != "" {
		lines = append(lines, "当前来源："+state.Source)
	}
	if state.LastError != "" {
		lines = append(lines, "最近连接/采集异常："+state.LastError)
	}
	if state.Source == "" && state.LastError == "" {
		lines = append(lines, "状态：等待下一次采集连接")
	}
	return strings.Join(lines, "\n")
}

func (s *session) captureControls(w fyne.Window) fyne.CanvasObject {
	base, cancelView := context.WithCancel(context.Background())
	s.captureCancel = cancelView
	s.mu.Lock()
	current := s.cfg.Capture.MuMu
	s.mu.Unlock()

	root := widget.NewEntry()
	root.SetPlaceHolder("选择 MuMu 安装根目录；选择后需点击保存并应用")
	root.SetText(current.InstallDir)
	number := widget.NewEntry()
	number.SetText(strconv.Itoa(current.Instance))

	status := widget.NewLabel("正在扫描运行中的 MuMu 安装与实例…")
	status.Wrapping = fyne.TextWrapWord
	applied := widget.NewLabel("")
	applied.Wrapping = fyne.TextWrapWord
	draftLabel := widget.NewLabel("待保存：" + captureTargetText(current))
	draftLabel.Wrapping = fyne.TextWrapWord

	var items []mumu.ProcessChoice
	selected := 0
	draft := current
	var choices *widget.List
	var generation uint64

	refreshApplied := func() {
		s.mu.Lock()
		saved := s.cfg.Capture.MuMu
		path := s.cfgPath
		s.mu.Unlock()
		applied.SetText(captureStateText(path, saved, s.snapshotCaptureState()))
	}
	refreshDraft := func() { draftLabel.SetText("待保存：" + captureTargetText(draft)) }

	applyTarget := func(next config.MuMuCaptureConfig, label string) {
		s.mu.Lock()
		cfg := s.cfg
		path := s.cfgPath
		s.mu.Unlock()
		cfg.Capture.MuMu = next
		if err := config.Save(path, cfg); err != nil {
			dialog.ShowError(err, w)
			return
		}
		s.mu.Lock()
		s.cfg = cfg
		s.left.Reset()
		s.right.Reset()
		s.clearAutoIdentity()
		s.ninjaDisplays = [2]ninjaDisplay{}
		control := s.captureControl
		s.mu.Unlock()
		revision := uint64(0)
		if control != nil {
			revision = control(next)
			s.mu.Lock()
			s.sourceRevision = revision
			s.mu.Unlock()
		}
		current, draft = next, next
		refreshDraft()
		status.SetText("已保存并请求应用：" + label + "。采集器将在下一次采集时切换。")
		refreshApplied()
		s.refreshOverlay()
	}

	setDraftFromSelected := func() {
		if selected == 0 {
			draft.Selection = "auto"
			draft.InstallDir = ""
			draft.Instance = 0
			draft.DLLPath = ""
			refreshDraft()
			status.SetText("已选择自动检测草稿；点击“使用选中的进程”后保存并应用。")
			return
		}
		if selected > len(items) {
			return
		}
		item := items[selected-1]
		draft.Selection = "manual"
		draft.InstallDir = item.Root
		draft.Instance = item.Index
		draft.DLLPath = ""
		root.SetText(item.Root)
		number.SetText(strconv.Itoa(item.Index))
		refreshDraft()
		status.SetText("已选择草稿：" + captureTargetText(draft) + "。点击“使用选中的进程”后保存并应用。")
	}

	choices = widget.NewList(func() int { return len(items) + 1 }, func() fyne.CanvasObject {
		title := widget.NewLabelWithStyle("MuMu 安卓设备", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		details := widget.NewLabel("安装目录、实例号和进程信息")
		details.Wrapping = fyne.TextWrapWord
		return container.NewBorder(nil, nil, widget.NewIcon(theme.ComputerIcon()), nil, container.NewVBox(title, details))
	}, func(id widget.ListItemID, obj fyne.CanvasObject) {
		group := obj.(*fyne.Container)
		labels := group.Objects[0].(*fyne.Container)
		title := labels.Objects[0].(*widget.Label)
		detail := labels.Objects[1].(*widget.Label)
		if id == 0 {
			title.SetText("自动检测（推荐）")
			detail.SetText("扫描所有运行中的 MuMu 安装；多实例无法唯一确认时需要手动选择。")
			return
		}
		item := items[id-1]
		state := "未启动"
		if item.AndroidStarted {
			state = "Android 已启动"
		} else if item.ProcessStarted || item.Running {
			state = "进程已启动"
		}
		title.SetText(fmt.Sprintf("%s · 实例 %d", item.Name, item.Index))
		details := []string{item.Root, state}
		if item.PID > 0 {
			details = append(details, fmt.Sprintf("PID %d", item.PID))
		}
		if item.ProcessName != "" {
			details = append(details, item.ProcessName)
		}
		detail.SetText(strings.Join(details, " · "))
	})
	choices.OnSelected = func(id widget.ListItemID) {
		selected = id
		setDraftFromSelected()
	}

	refresh := widget.NewButtonWithIcon("扫描 MuMu 安装与实例", theme.ViewRefreshIcon(), nil)
	refresh.OnTapped = func() {
		generation++
		now := generation
		refresh.Disable()
		status.SetText("正在扫描 MuMu 安装目录、实例号和窗口进程…")
		// A manually typed directory is treated as an explicit scan hint. An
		// empty entry scans every currently running MuMu installation.
		directory := strings.TrimSpace(root.Text)
		go func() {
			ctx, cancel := context.WithTimeout(base, 12*time.Second)
			defer cancel()
			found, err := mumu.ListProcessChoices(ctx, directory)
			fyne.Do(func() {
				if base.Err() != nil || s.stopped() || s.settings != w || now != generation {
					return
				}
				refresh.Enable()
				if err != nil {
					status.SetText("未找到可选实例。可手动填写目录和实例编号后保存，或附诊断包反馈。\n" + err.Error())
					return
				}
				items = found
				choices.Refresh()
				selected = 0
				for index, item := range items {
					if (current.Selection == "manual" || (current.Selection == "" && current.InstallDir != "")) &&
						item.Index == current.Instance && strings.EqualFold(item.Root, current.InstallDir) {
						selected = index + 1
						break
					}
				}
				handler := choices.OnSelected
				choices.OnSelected = nil
				choices.Select(selected)
				choices.OnSelected = handler
				status.SetText(fmt.Sprintf("找到 %d 个可选实例。相同的实例编号在不同安装目录下会分别显示。", len(items)))
				refreshApplied()
			})
		}()
	}
	s.captureRefresh = refresh.OnTapped

	browse := widget.NewButton("浏览安装目录", func() {
		dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if uri == nil {
				return
			}
			root.SetText(uri.Path())
			draft.Selection = "manual"
			draft.InstallDir = uri.Path()
			draft.DLLPath = ""
			refreshDraft()
			status.SetText("目录已选择但尚未保存；填写实例编号后点击“保存并应用手动实例”。")
		}, w).Show()
	})

	saveSelected := widget.NewButton("使用选中的进程", func() {
		if selected == 0 {
			draft.Selection = "auto"
			draft.InstallDir = ""
			draft.Instance = 0
			draft.DLLPath = ""
		}
		applyTarget(draft, captureTargetText(draft))
	})
	saveSelected.Importance = widget.HighImportance
	saveManual := widget.NewButton("保存并应用手动实例", func() {
		next, err := selectionFromFields(root.Text, number.Text, draft)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		applyTarget(next, captureTargetText(next))
	})

	var test *widget.Button
	test = widget.NewButton("测试当前选择（不保存）", func() {
		next, err := captureProbeSelection(selected, items, root.Text, number.Text, draft)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		test.Disable()
		status.SetText("正在通过独立进程检查 MuMu SDK 连接与截图…")
		executable := s.executablePath
		if executable == "" {
			executable, _ = os.Executable()
		}
		go func() {
			ctx, cancel := context.WithTimeout(base, 15*time.Second)
			defer cancel()
			result, runErr := support.ProbeSDK(ctx, executable, mumu.Options{
				InstallDir: next.InstallDir, DLLPath: next.DLLPath, Instance: next.Instance,
				DisplayID: next.DisplayID, Package: next.Package,
			})
			fyne.Do(func() {
				if base.Err() != nil || s.stopped() || s.settings != w {
					return
				}
				test.Enable()
				if runErr != nil {
					status.SetText("SDK 自检未完成：" + runErr.Error())
					return
				}
				if result.Error != "" {
					status.SetText("SDK 自检失败阶段：" + result.FailureStage + "\n" + result.Error)
					return
				}
				status.SetText(fmt.Sprintf("SDK 自检成功：已读取 %d × %d 图像。该测试未修改已保存配置。", result.Width, result.Height))
			})
		}()
	})

	manual := widget.NewAccordion(widget.NewAccordionItem("高级设置：手动指定安装目录与实例", container.NewVBox(
		widget.NewLabel("MuMu 安装目录（选择后不会自动保存）"), root, browse,
		widget.NewLabel("多开管理器中的实例编号（0、1、2…）"), number,
		container.NewGridWithColumns(2, saveManual, test),
	)))
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(0, 250))
	refreshApplied()
	refresh.OnTapped()
	return container.NewVScroll(container.NewVBox(
		sectionCard("选择模拟器进程", container.NewVBox(
			applied, draftLabel, status, refresh,
			container.NewStack(spacer, choices), saveSelected,
		)),
		manual,
	))
}
