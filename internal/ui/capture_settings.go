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

	"narutotimer/internal/capture"
	"narutotimer/internal/capture/leidian"
	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"narutotimer/internal/support"
)

func WithCaptureSelection(selectTarget func(config.MuMuCaptureConfig) uint64) Option {
	return func(s *session) { s.captureControl = selectTarget }
}

func WithCaptureConfigSelection(selectTarget func(config.CaptureConfig) uint64) Option {
	return func(s *session) { s.captureConfigControl = selectTarget }
}

func WithInitialAbout(show bool) Option { return func(s *session) { s.initialAbout = show } }

var captureProviderLabels = map[string]string{
	"auto":                    "自动（按回退顺序）",
	capture.MethodMuMuSDK:     "MuMu SDK",
	capture.MethodLeidianADB:  "雷电 ADB",
	capture.MethodPrintWindow: "窗口截图（PrintWindow）",
}

func captureProvider(c config.CaptureConfig) string {
	if c.Provider != "" {
		return c.Provider
	}
	if len(c.PreferredMethods) > 0 {
		return c.PreferredMethods[0]
	}
	return capture.MethodMuMuSDK
}

func captureProviderLabel(method string) string {
	if label := captureProviderLabels[method]; label != "" {
		return label
	}
	return captureProviderLabels[capture.MethodMuMuSDK]
}

func captureProviderFromLabel(label string) string {
	switch label {
	case "自动（按回退顺序）":
		return "auto"
	case "MuMu SDK":
		return capture.MethodMuMuSDK
	case "雷电 ADB":
		return capture.MethodLeidianADB
	case "窗口截图（PrintWindow）":
		return capture.MethodPrintWindow
	default:
		return capture.MethodMuMuSDK
	}
}

// captureConfigWithProvider changes only the explicitly selected provider. In
// particular, auto keeps its configured fallback order in PreferredMethods.
func captureConfigWithProvider(draft config.CaptureConfig, method string) config.CaptureConfig {
	draft.Provider = method
	return draft
}

func captureDiscoveryCanApply(view, activeView fyne.Window, generation, currentGeneration uint64, scannedProvider, activeProvider string) bool {
	return view == activeView && generation == currentGeneration && scannedProvider == activeProvider
}

func captureTargetText(c config.CaptureConfig) string {
	switch captureProvider(c) {
	case "auto":
		if len(c.PreferredMethods) == 0 {
			return "自动选择 · 使用内置回退顺序"
		}
		return "自动选择 · " + strings.Join(c.PreferredMethods, " → ")
	case capture.MethodPrintWindow:
		return "窗口截图 · PrintWindow 全内容"
	case capture.MethodLeidianADB:
		target := c.Leidian
		if target.Selection == "auto" || (target.Selection == "" && target.InstallDir == "" && target.Index == 0 && target.Serial == "") {
			return "雷电 · 自动检测"
		}
		label := strings.TrimSpace(target.InstallDir)
		if label == "" {
			label = strings.TrimSpace(target.Serial)
		}
		if label == "" {
			label = "未指定安装目录"
		}
		return fmt.Sprintf("雷电 · %s · 实例 %d", label, target.Index)
	}
	target := c.MuMu
	if target.Selection == "auto" || (target.Selection == "" && target.InstallDir == "" && target.Instance == 0 && target.DLLPath == "") {
		return "MuMu · 自动检测"
	}
	root := strings.TrimSpace(target.InstallDir)
	if root == "" {
		root = "未指定安装目录"
	}
	return fmt.Sprintf("MuMu · %s · 实例 %d", root, target.Instance)
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

func leidianSelectionFromFields(root, index, serial string, base config.LeidianCaptureConfig) (config.LeidianCaptureConfig, error) {
	id, err := strconv.Atoi(strings.TrimSpace(index))
	if err != nil || id < 0 {
		return config.LeidianCaptureConfig{}, fmt.Errorf("雷电实例编号需要填写非负整数")
	}
	base.Selection = "manual"
	base.InstallDir = strings.TrimSpace(root)
	base.Index = id
	base.Serial = strings.TrimSpace(serial)
	if base.InstallDir == "" && base.ConsolePath == "" {
		return config.LeidianCaptureConfig{}, fmt.Errorf("请先填写雷电安装目录或 ldconsole.exe 路径")
	}
	if base.Serial == "" {
		base.Serial = fmt.Sprintf("127.0.0.1:%d", leidian.DefaultADBSerialBasePort+id)
	}
	return base, nil
}

func captureConfigForMuMuProbe(draft config.CaptureConfig, target config.MuMuCaptureConfig) config.CaptureConfig {
	draft.Provider = capture.MethodMuMuSDK
	draft.MuMu = target
	return draft
}

func captureConfigForLeidianProbe(draft config.CaptureConfig, target config.LeidianCaptureConfig) config.CaptureConfig {
	draft.Provider = capture.MethodLeidianADB
	draft.Leidian = target
	return draft
}

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
	captureCfg := s.cfg.Capture
	source := s.captureSource
	s.mu.Unlock()
	return frame.CaptureState{Requested: captureCfg.MuMu, Applied: captureCfg.MuMu, RequestedCapture: captureCfg, AppliedCapture: captureCfg, Source: source}
}

func captureStateText(path string, saved any, state frame.CaptureState) string {
	var savedConfig config.CaptureConfig
	switch value := saved.(type) {
	case config.CaptureConfig:
		savedConfig = value
	case config.MuMuCaptureConfig:
		savedConfig = config.CaptureConfig{Provider: capture.MethodMuMuSDK, PreferredMethods: []string{capture.MethodMuMuSDK}, MuMu: value}
	default:
		savedConfig = config.Default().Capture
	}
	var lines []string
	lines = append(lines, "配置文件："+path)
	lines = append(lines, "已保存："+captureTargetText(savedConfig))
	if state.RequestedRevision > 0 {
		requested := state.RequestedCapture
		if requested.Provider == "" {
			requested = config.CaptureConfig{Provider: capture.MethodMuMuSDK, PreferredMethods: []string{capture.MethodMuMuSDK}, MuMu: state.Requested}
		}
		applied := state.AppliedCapture
		if applied.Provider == "" {
			applied = config.CaptureConfig{Provider: capture.MethodMuMuSDK, PreferredMethods: []string{capture.MethodMuMuSDK}, MuMu: state.Applied}
		}
		lines = append(lines, fmt.Sprintf("已请求应用：%s（修订 %d）", captureTargetText(requested), state.RequestedRevision))
		lines = append(lines, fmt.Sprintf("采集器已应用：%s（修订 %d）", captureTargetText(applied), state.AppliedRevision))
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
	current := s.cfg.Capture
	s.mu.Unlock()

	provider := widget.NewSelect([]string{
		captureProviderLabel("auto"),
		captureProviderLabel(capture.MethodMuMuSDK),
		captureProviderLabel(capture.MethodLeidianADB),
		captureProviderLabel(capture.MethodPrintWindow),
	}, nil)
	provider.Selected = captureProviderLabel(captureProvider(current))

	mumuRoot := widget.NewEntry()
	mumuRoot.SetPlaceHolder("选择 MuMu 安装根目录")
	mumuRoot.SetText(current.MuMu.InstallDir)
	mumuNumber := widget.NewEntry()
	mumuNumber.SetText(strconv.Itoa(current.MuMu.Instance))

	ldRoot := widget.NewEntry()
	ldRoot.SetPlaceHolder("例如 E:\\leidian\\LDPlayer14")
	ldRoot.SetText(current.Leidian.InstallDir)
	if strings.TrimSpace(ldRoot.Text) == "" {
		if discovered := leidian.DiscoverInstallDir(); discovered != "" {
			ldRoot.SetText(discovered)
			current.Leidian.InstallDir = discovered
		}
	}
	ldConsole := widget.NewEntry()
	ldConsole.SetPlaceHolder("可选：ldconsole.exe / dnconsole.exe")
	ldConsole.SetText(current.Leidian.ConsolePath)
	ldADB := widget.NewEntry()
	ldADB.SetPlaceHolder("可选：adb.exe；默认从安装目录寻找")
	ldADB.SetText(current.Leidian.ADBPath)
	ldIndex := widget.NewEntry()
	ldIndex.SetText(strconv.Itoa(current.Leidian.Index))
	ldSerial := widget.NewEntry()
	ldSerial.SetPlaceHolder("留空自动使用 127.0.0.1:5555 + 实例编号")
	ldSerial.SetText(current.Leidian.Serial)
	ldFinalSerial := widget.NewLabel("")
	ldFinalSerial.Wrapping = fyne.TextWrapWord
	refreshLDFinalSerial := func() {
		serial := strings.TrimSpace(ldSerial.Text)
		index, err := strconv.Atoi(strings.TrimSpace(ldIndex.Text))
		if serial == "" && err == nil && index >= 0 {
			serial = fmt.Sprintf("127.0.0.1:%d", leidian.DefaultADBSerialBasePort+index)
		}
		if serial == "" {
			serial = "请先填写非负实例编号"
		}
		ldFinalSerial.SetText("最终使用 serial：" + serial)
	}
	ldIndex.OnChanged = func(string) { refreshLDFinalSerial() }
	ldSerial.OnChanged = func(string) { refreshLDFinalSerial() }
	refreshLDFinalSerial()
	ldPackage := widget.NewEntry()
	ldPackage.SetPlaceHolder("可选：Android 包名")
	ldPackage.SetText(current.Leidian.Package)
	ldConnect := widget.NewCheck("启动时自动连接 ADB", nil)
	ldConnect.SetChecked(current.Leidian.ConnectOnStart)

	status := widget.NewLabel("正在扫描模拟器实例…")
	status.Wrapping = fyne.TextWrapWord
	applied := widget.NewLabel("")
	applied.Wrapping = fyne.TextWrapWord
	draftLabel := widget.NewLabel("待保存：" + captureTargetText(current))
	draftLabel.Wrapping = fyne.TextWrapWord

	var muItems []mumu.ProcessChoice
	var ldItems []leidian.Instance
	selected := 0
	ldSelected := -1
	selectingDiscovered := false
	draft := current
	var choices *widget.List
	var generation uint64

	refreshApplied := func() {
		s.mu.Lock()
		saved := s.cfg.Capture
		path := s.cfgPath
		s.mu.Unlock()
		applied.SetText(captureStateText(path, saved, s.snapshotCaptureState()))
	}
	refreshDraft := func() { draftLabel.SetText("待保存：" + captureTargetText(draft)) }
	var muManual, ldManual *widget.Accordion
	setVisible := func() {
		method := captureProviderFromLabel(provider.Selected)
		isLD := method == capture.MethodLeidianADB
		isMuMu := method == capture.MethodMuMuSDK
		if isLD {
			ldRoot.Show()
			ldConsole.Show()
			ldADB.Show()
			ldIndex.Show()
			ldSerial.Show()
			ldPackage.Show()
			ldConnect.Show()
			mumuRoot.Hide()
			mumuNumber.Hide()
		} else {
			ldRoot.Hide()
			ldConsole.Hide()
			ldADB.Hide()
			ldIndex.Hide()
			ldSerial.Hide()
			ldPackage.Hide()
			ldConnect.Hide()
			if isMuMu {
				mumuRoot.Show()
				mumuNumber.Show()
			} else {
				mumuRoot.Hide()
				mumuNumber.Hide()
			}
		}
		if muManual != nil && ldManual != nil {
			if isLD {
				ldManual.Open(0)
				muManual.Close(0)
			} else if isMuMu {
				muManual.Open(0)
				ldManual.Close(0)
			} else {
				muManual.Close(0)
				ldManual.Close(0)
			}
		}
		refreshDraft()
	}

	applyConfig := func(next config.CaptureConfig, label string) {
		s.mu.Lock()
		cfg := s.cfg
		path := s.cfgPath
		s.mu.Unlock()
		cfg.Capture = next
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
		legacyControl := s.captureControl
		configControl := s.captureConfigControl
		s.mu.Unlock()
		revision := uint64(0)
		if configControl != nil {
			revision = configControl(next)
		} else if legacyControl != nil {
			revision = legacyControl(next.MuMu)
		}
		if revision > 0 {
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

	var refresh *widget.Button
	selectProvider := func(name string) {
		method := captureProviderFromLabel(name)
		draft = captureConfigWithProvider(draft, method)
		refreshDraft()
		setVisible()
		if method == capture.MethodLeidianADB && refresh != nil {
			// Provider switching is immediately actionable; it never saves.
			refresh.OnTapped()
		}
	}
	provider.OnChanged = selectProvider

	// Do not call selectProvider here: opening auto or PrintWindow must retain
	// its configured provider and PreferredMethods unchanged.
	setVisible()

	setMuDraftFromSelected := func() {
		if selected == 0 {
			draft.Provider = capture.MethodMuMuSDK
			draft.MuMu.Selection = "auto"
			draft.MuMu.InstallDir = ""
			draft.MuMu.Instance = 0
			draft.MuMu.DLLPath = ""
		} else if selected <= len(muItems) {
			item := muItems[selected-1]
			draft.Provider = capture.MethodMuMuSDK
			draft.MuMu.Selection = "manual"
			draft.MuMu.InstallDir = item.Root
			draft.MuMu.Instance = item.Index
			draft.MuMu.DLLPath = ""
			mumuRoot.SetText(item.Root)
			mumuNumber.SetText(strconv.Itoa(item.Index))
		}
		refreshDraft()
	}

	choices = widget.NewList(func() int {
		method := captureProviderFromLabel(provider.Selected)
		if method == capture.MethodLeidianADB {
			return len(ldItems) + 1
		}
		if method != capture.MethodMuMuSDK {
			return 0
		}
		return len(muItems) + 1
	}, func() fyne.CanvasObject {
		title := widget.NewLabelWithStyle("模拟器实例", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		details := widget.NewLabel("安装目录、实例号和连接状态")
		details.Wrapping = fyne.TextWrapWord
		return container.NewBorder(nil, nil, widget.NewIcon(theme.ComputerIcon()), nil, container.NewVBox(title, details))
	}, func(id widget.ListItemID, obj fyne.CanvasObject) {
		group := obj.(*fyne.Container)
		labels := group.Objects[0].(*fyne.Container)
		title := labels.Objects[0].(*widget.Label)
		detail := labels.Objects[1].(*widget.Label)
		if id == 0 {
			title.SetText("自动检测（推荐）")
			if captureProviderFromLabel(provider.Selected) == capture.MethodLeidianADB {
				detail.SetText("扫描雷电 list2；只有一个可连接实例时可自动选择。")
			} else {
				detail.SetText("扫描所有运行中的 MuMu 安装；多实例时请手动选择。")
			}
			return
		}
		if captureProviderFromLabel(provider.Selected) == capture.MethodLeidianADB {
			if id-1 >= len(ldItems) {
				return
			}
			item := ldItems[id-1]
			title.SetText(fmt.Sprintf("%s · 实例 %d", item.Name, item.Index))
			detail.SetText(strings.Join([]string{item.Root, item.Serial, item.Label()}, " · "))
			return
		}
		if id-1 >= len(muItems) {
			return
		}
		item := muItems[id-1]
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
		if selectingDiscovered {
			return
		}
		if captureProviderFromLabel(provider.Selected) == capture.MethodLeidianADB {
			ldSelected = id
			if id == 0 {
				draft.Leidian.Selection = "auto"
			} else if id <= len(ldItems) {
				item := ldItems[id-1]
				draft.Provider = capture.MethodLeidianADB
				draft.Leidian.Selection = "manual"
				draft.Leidian.InstallDir = item.Root
				draft.Leidian.Index = item.Index
				draft.Leidian.Serial = item.Serial
				ldRoot.SetText(item.Root)
				ldIndex.SetText(strconv.Itoa(item.Index))
				ldSerial.SetText(item.Serial)
			}
			refreshDraft()
			return
		}
		selected = id
		setMuDraftFromSelected()
	}

	refresh = widget.NewButtonWithIcon("扫描模拟器实例", theme.ViewRefreshIcon(), nil)
	refresh.OnTapped = func() {
		scannedProvider := captureProviderFromLabel(provider.Selected)
		if scannedProvider != capture.MethodMuMuSDK && scannedProvider != capture.MethodLeidianADB {
			status.SetText("当前后端不需要实例扫描；选择 MuMu SDK 或雷电 ADB 可查看并选择已发现的实例。")
			choices.Refresh()
			return
		}
		generation++
		now := generation
		refresh.Disable()
		isLD := scannedProvider == capture.MethodLeidianADB
		if isLD {
			status.SetText("正在调用雷电 list2 和 isrunning 扫描实例…")
		} else {
			status.SetText("正在扫描 MuMu 安装目录、实例号和窗口进程…")
		}
		go func() {
			ctx, cancel := context.WithTimeout(base, 15*time.Second)
			defer cancel()
			var muFound []mumu.ProcessChoice
			var ldFound []leidian.Instance
			var err error
			if isLD {
				ldFound, err = leidian.ListInstances(ctx, strings.TrimSpace(ldRoot.Text))
			} else {
				muFound, err = mumu.ListProcessChoices(ctx, strings.TrimSpace(mumuRoot.Text))
			}
			fyne.Do(func() {
				if base.Err() != nil || s.stopped() || !captureDiscoveryCanApply(w, s.settings, now, generation, scannedProvider, captureProviderFromLabel(provider.Selected)) {
					return
				}
				refresh.Enable()
				if err != nil {
					status.SetText("未找到可选实例。请检查安装目录和模拟器是否已安装。\n" + err.Error())
					return
				}
				if isLD {
					ldItems = ldFound
					choices.Refresh()
					ldSelected = 0
					for index, item := range ldItems {
						if (current.Leidian.Selection == "manual" || (current.Leidian.Selection == "" && current.Leidian.InstallDir != "")) && item.Index == current.Leidian.Index && strings.EqualFold(item.Root, current.Leidian.InstallDir) {
							ldSelected = index + 1
							break
						}
					}
					selectingDiscovered = true
					choices.Select(ldSelected)
					selectingDiscovered = false
					if len(ldItems) == 1 && current.Leidian.Selection == "auto" {
						status.SetText("雷电找到 1 个实例；请选择该行后测试或保存并应用。")
					} else {
						status.SetText(fmt.Sprintf("雷电找到 %d 个实例。实例 %d 默认 serial：%s", len(ldItems), current.Leidian.Index, func() string {
							if current.Leidian.Serial != "" {
								return current.Leidian.Serial
							}
							return fmt.Sprintf("127.0.0.1:%d", leidian.DefaultADBSerialBasePort+current.Leidian.Index)
						}()))
					}
				} else {
					muItems = muFound
					choices.Refresh()
					selected = 0
					for index, item := range muItems {
						if (current.MuMu.Selection == "manual" || (current.MuMu.Selection == "" && current.MuMu.InstallDir != "")) && item.Index == current.MuMu.Instance && strings.EqualFold(item.Root, current.MuMu.InstallDir) {
							selected = index + 1
							break
						}
					}
					selectingDiscovered = true
					choices.Select(selected)
					selectingDiscovered = false
					status.SetText(fmt.Sprintf("MuMu 找到 %d 个可选实例。", len(muItems)))
				}
				refreshApplied()
			})
		}()
	}
	s.captureRefresh = refresh.OnTapped

	browseMu := widget.NewButton("浏览 MuMu 目录", func() {
		dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if uri == nil {
				return
			}
			mumuRoot.SetText(uri.Path())
			draft.Provider = capture.MethodMuMuSDK
			draft.MuMu.Selection = "manual"
			draft.MuMu.InstallDir = uri.Path()
			draft.MuMu.DLLPath = ""
			refreshDraft()
		}, w).Show()
	})
	browseLD := widget.NewButton("浏览雷电目录", func() {
		dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if uri == nil {
				return
			}
			ldRoot.SetText(uri.Path())
			draft.Leidian.InstallDir = uri.Path()
			draft.Leidian.Selection = "manual"
			refreshDraft()
		}, w).Show()
	})

	saveSelected := widget.NewButton("保存并应用", func() {
		method := captureProviderFromLabel(provider.Selected)
		if method == capture.MethodLeidianADB {
			// A selected list item and the visible fields describe the same target.
			// Rebuild manual targets from those fields so edits require a new probe
			// during onboarding instead of silently saving the old verified target.
			if ldSelected > 0 || draft.Leidian.Selection == "manual" {
				next, err := leidianSelectionFromFields(ldRoot.Text, ldIndex.Text, ldSerial.Text, draft.Leidian)
				if err != nil {
					dialog.ShowError(err, w)
					return
				}
				next.ConsolePath = strings.TrimSpace(ldConsole.Text)
				next.ADBPath = strings.TrimSpace(ldADB.Text)
				next.Package = strings.TrimSpace(ldPackage.Text)
				next.ConnectOnStart = ldConnect.Checked
				draft.Provider = capture.MethodLeidianADB
				draft.Leidian = next
			}
			applyConfig(draft, captureTargetText(draft))
			return
		}
		if method != capture.MethodMuMuSDK {
			applyConfig(draft, captureTargetText(draft))
			return
		}
		// A successful single-instance MuMu probe resolves the automatic choice
		// to a manual target. Preserve that target and rebuild it from visible
		// fields so post-probe edits cannot bypass the current-target check.
		if selected > 0 || draft.MuMu.Selection == "manual" {
			next, err := selectionFromFields(mumuRoot.Text, mumuNumber.Text, draft.MuMu)
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			draft.Provider = capture.MethodMuMuSDK
			draft.MuMu = next
		}
		applyConfig(draft, captureTargetText(draft))
	})
	saveSelected.Importance = widget.HighImportance

	var test *widget.Button
	test = widget.NewButton("测试当前选择（不保存）", func() {
		method := captureProviderFromLabel(provider.Selected)
		if method == capture.MethodLeidianADB {
			next, err := leidianSelectionFromFields(ldRoot.Text, ldIndex.Text, ldSerial.Text, draft.Leidian)
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			next.ConsolePath = strings.TrimSpace(ldConsole.Text)
			next.ADBPath = strings.TrimSpace(ldADB.Text)
			next.Package = strings.TrimSpace(ldPackage.Text)
			next.ConnectOnStart = ldConnect.Checked
			test.Disable()
			status.SetText("正在连接雷电 ADB 并读取一帧截图…")
			go func() {
				ctx, cancel := context.WithTimeout(base, 18*time.Second)
				defer cancel()
				result := leidian.ProbeContext(ctx, leidian.Options{InstallDir: next.InstallDir, ConsolePath: next.ConsolePath, ADBPath: next.ADBPath, Index: next.Index, Serial: next.Serial, Package: next.Package, Connect: true})
				fyne.Do(func() {
					if base.Err() != nil || s.stopped() || s.settings != w {
						return
					}
					test.Enable()
					if result.Error != "" {
						status.SetText("雷电 ADB 自检失败：" + result.Error)
						return
					}
					draft = captureConfigForLeidianProbe(draft, next)
					refreshDraft()
					status.SetText(fmt.Sprintf("雷电 ADB 自检成功：已读取 %d × %d 图像。该测试未修改已保存配置。", result.Width, result.Height))
				})
			}()
			return
		}
		if method != capture.MethodMuMuSDK {
			dialog.ShowInformation("无需单独自检", "自动选择和窗口截图将在下一次采集时按当前配置尝试；此处不保存配置。", w)
			return
		}
		next, err := captureProbeSelection(selected, muItems, mumuRoot.Text, mumuNumber.Text, draft.MuMu)
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
			result, runErr := support.ProbeSDK(ctx, executable, mumu.Options{InstallDir: next.InstallDir, DLLPath: next.DLLPath, Instance: next.Instance, DisplayID: next.DisplayID, Package: next.Package})
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
				draft = captureConfigForMuMuProbe(draft, next)
				mumuRoot.SetText(next.InstallDir)
				mumuNumber.SetText(strconv.Itoa(next.Instance))
				refreshDraft()
				status.SetText(fmt.Sprintf("SDK 自检成功：已读取 %d × %d 图像。该测试未修改已保存配置。", result.Width, result.Height))
			})
		}()
	})

	providerForm := container.NewVBox(widget.NewLabel("采集后端"), provider, widget.NewLabel("自动按回退顺序选择；MuMu 与雷电可直接扫描实例；窗口截图使用 PrintWindow。"))
	muDetails := container.NewVBox(
		widget.NewLabel("MuMu 安装目录"), mumuRoot, browseMu,
		widget.NewLabel("多开管理器中的实例编号（实例 0/1：第 1/2 个多开）"), mumuNumber,
	)
	ldDetails := container.NewVBox(
		widget.NewLabel("雷电安装目录"), ldRoot, browseLD,
		widget.NewLabel("ldconsole.exe / dnconsole.exe 路径（可选）"), ldConsole,
		widget.NewLabel("adb.exe 路径（可选）"), ldADB,
		widget.NewLabel("实例编号（实例 0/1：第 1/2 个多开；也可填其他非负编号）"), ldIndex,
		widget.NewLabel("ADB serial（留空自动映射 127.0.0.1:5555 + index）"), ldSerial, ldFinalSerial,
		widget.NewLabel("游戏包名（可选）"), ldPackage, ldConnect,
	)
	// Advanced settings contain only manual overrides; primary actions stay above.
	// The inactive section is collapsed, not hidden, so switching provider never
	// makes one adapter look unsupported or second-class.
	muManual = widget.NewAccordion(widget.NewAccordionItem("MuMu：高级设置", muDetails))
	ldManual = widget.NewAccordion(widget.NewAccordionItem("雷电：高级设置", ldDetails))
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(0, 250))
	refreshApplied()
	setVisible()
	if method := captureProviderFromLabel(provider.Selected); method == capture.MethodMuMuSDK || method == capture.MethodLeidianADB {
		refresh.OnTapped()
	}
	return container.NewVScroll(container.NewVBox(
		sectionCard("选择模拟器进程", container.NewVBox(providerForm, applied, draftLabel, status, refresh, container.NewStack(spacer, choices), container.NewGridWithColumns(2, test, saveSelected))),
		muManual,
		ldManual,
	))
}
