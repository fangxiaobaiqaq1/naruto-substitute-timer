package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/config"
	"narutotimer/internal/identity"
	"narutotimer/internal/ninja"
)

const identityFreshFor = 2 * time.Second

func manualSide(side string) bool { return side == "left" || side == "right" }

func sideMode(label string) string {
	switch label {
	case "左边", "left":
		return "left"
	case "右边", "right":
		return "right"
	default:
		return "auto"
	}
}

func sideLabel(mode string) string {
	switch mode {
	case "left":
		return "左边"
	case "right":
		return "右边"
	default:
		return "自动认边"
	}
}

func (s *session) restoreSide() {
	s.remember = s.cfg.UI.RememberSide
	mode := sideMode(s.cfg.UI.PlayerSide)
	if !s.remember {
		mode = "auto"
	}
	s.setSideModeLocked(mode)
}

// The caller holds s.mu (or is initializing an unpublished session).
func (s *session) setSideModeLocked(mode string) {
	oldMode := sideMode(s.cfg.UI.PlayerSide)
	s.cfg.UI.PlayerSide = mode
	if manualSide(mode) {
		if s.side != mode {
			s.oppName = ""
		}
		s.side = mode
		return
	}
	// Saving other settings in auto does not discard a valid VS recognition.
	if oldMode == "auto" && manualSide(s.side) {
		return
	}
	s.side, s.oppName = "", ""
	if manualSide(s.autoIdentity.Side) && time.Since(s.autoIdentityAt) < identityFreshFor {
		s.side = s.autoIdentity.Side
		s.oppName = s.autoIdentity.Opp
		if s.autoIdentity.Mine != "" {
			s.myName = s.autoIdentity.Mine
		}
	}
}

func (s *session) clearAutoIdentity() {
	s.autoIdentity = identity.Readout{}
	s.autoIdentityAt = time.Time{}
	if !manualSide(s.cfg.UI.PlayerSide) {
		s.side, s.oppName = "", ""
	}
}

// settingsSnapshotLocked captures the settings to persist; the caller holds
// s.mu and calls Save on the result after releasing it. The capture loop takes
// s.mu on every frame, so the disk write (fsync, antivirus) must not run under
// it. Snapshots are ordered by s.cfgWriter: an older one never overwrites the
// user's more recent selection.
func (s *session) settingsSnapshotLocked() config.Snapshot {
	cfg := s.cfg
	cfg.UI.RememberSide = s.remember
	if !s.remember {
		cfg.UI.PlayerSide = "auto"
	}
	return s.cfgWriter.Snapshot(s.cfgPath, cfg)
}

func (s *session) selectSideMode(mode string) error {
	mode = sideMode(mode)
	s.mu.Lock()
	s.setSideModeLocked(mode)
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	err := snapshot.Save()
	s.syncSettingsSide()
	s.refreshOverlay()
	return err
}

func (s *session) setRememberSide(remember bool) error {
	s.mu.Lock()
	s.remember, s.cfg.UI.RememberSide = remember, remember
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	return snapshot.Save()
}

func (s *session) syncSettingsSide() {
	if s.settingsSide == nil {
		return
	}
	s.mu.Lock()
	label := sideLabel(s.cfg.UI.PlayerSide)
	s.mu.Unlock()
	if s.settingsSide.Selected != label {
		onChanged := s.settingsSide.OnChanged
		s.settingsSide.OnChanged = nil
		s.settingsSide.SetSelected(label)
		s.settingsSide.OnChanged = onChanged
	}
}

func showSettingsError(err error, w fyne.Window) {
	if err != nil && w != nil {
		dialog.ShowError(fmt.Errorf("设置已应用，但保存失败：%w", err), w)
	}
}

// settingsWindow is where errors from overlay/mini actions are shown: the
// open settings window if any, else the full overlay.
func (s *session) dialogWindow() fyne.Window {
	if s.settings != nil {
		return s.settings
	}
	return s.win
}

// openSettings shows the settings window. An existing window is only brought
// forward: rebuilding it would cancel a running update download or emulator
// scan and lose the selected tab. Live-applied controls are kept in sync by
// syncSettingsControls, so nothing in it can be stale.
func (s *session) openSettings() {
	if s.settings != nil && s.settingsTabs != nil {
		s.syncSettingsControls()
		s.settings.Show()
		s.settings.RequestFocus()
		return
	}
	s.buildSettings(0)
}

// rebuildSettings replaces the content in place (e.g. after the update source
// changes), keeping the selected tab.
func (s *session) rebuildSettings() {
	tab := 0
	if s.settingsTabs != nil {
		tab = s.settingsTabs.SelectedIndex()
	}
	s.buildSettings(tab)
}

func (s *session) buildSettings(tab int) {
	w := s.settings
	if w == nil {
		w = fyne.CurrentApp().NewWindow("替身设置")
		s.settings = w
		addToggleMiniShortcut(w, s)
		w.SetOnClosed(func() {
			if s.captureCancel != nil {
				s.captureCancel()
			}
			if s.aboutCancel != nil {
				s.aboutCancel()
			}
			s.settings, s.settingsSide = nil, nil
			s.settingsMini, s.settingsBoth = nil, nil
			s.textStatusLabel = nil
			s.settingsTabs = nil
		})
	}
	s.mu.Lock()
	name := strings.Join(s.cfg.UI.PlayerNames, "，")
	ninjaText := s.cfg.UI.NinjaQuery
	mode, remember, top := s.cfg.UI.PlayerSide, s.remember, s.topmost
	autoText := s.cfg.UI.AutoTextRecognition
	opacity, fontScale := s.cfg.UI.WindowOpacity, s.cfg.UI.FontScale
	overlayMode, showBoth, gpuAccel := s.cfg.UI.OverlayMode, s.cfg.UI.ShowBothSides, s.cfg.UI.GPUAcceleration
	miniOpacity, miniAutoScale := s.cfg.UI.MiniOpacity, s.cfg.UI.MiniAutoScale
	appearanceError := s.appearanceError
	textStatus := s.textStatusText()
	s.mu.Unlock()

	wrapped := func(text string) *widget.Label {
		l := widget.NewLabel(text)
		l.Wrapping = fyne.TextWrapWord
		l.Importance = widget.LowImportance
		return l
	}

	// ---- 认边 ----
	sideSel := widget.NewRadioGroup([]string{"自动认边", "左边", "右边"}, nil)
	sideSel.Required, sideSel.Horizontal = true, true
	sideSel.SetSelected(sideLabel(mode))
	s.settingsSide = sideSel
	sideSel.OnChanged = func(label string) {
		showSettingsError(s.selectSideMode(sideMode(label)), w)
	}
	rememberChk := widget.NewCheck("记住认边方式（下次启动沿用）", nil)
	rememberChk.SetChecked(remember)
	rememberChk.OnChanged = func(checked bool) { showSettingsError(s.setRememberSide(checked), w) }

	// ---- 名字与忍者（点保存生效） ----
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("自己的账号名，用来自动认边；多个用逗号分隔")
	nameEntry.SetText(name)
	ninjaEntry := widget.NewSelectEntry([]string{"", ninja.FifthMizukage, ninja.Hashirama, ninja.Madara, ninja.Obito, ninja.Naruto, ninja.HanzoEdo})
	ninjaEntry.SetPlaceHolder("留空自动；或选择完整忍者版本")
	ninjaEntry.SetText(ninjaText)
	textCheck := widget.NewCheck("后台识别名字（内置本地 OCR）", nil)
	textCheck.SetChecked(autoText)
	textCheck.OnChanged = func(on bool) { showSettingsError(s.setTextRecognition(on), w) }
	s.textStatusLabel = wrapped(textStatus)
	gpuChk := widget.NewCheck("OCR 使用 GPU 加速（DirectML，重启生效）", nil)
	gpuChk.SetChecked(gpuAccel)
	gpuChk.OnChanged = func(on bool) {
		s.mu.Lock()
		s.cfg.UI.GPUAcceleration = on
		snapshot := s.settingsSnapshotLocked()
		s.mu.Unlock()
		showSettingsError(snapshot.Save(), w)
	}
	topChk := widget.NewCheck("窗口置顶", nil)
	topChk.SetChecked(top)
	save := widget.NewButtonWithIcon("保存名字与忍者", theme.DocumentSaveIcon(), func() {
		if err := s.applySettings(nameEntry.Text, ninjaEntry.Text, sideSel.Selected, rememberChk.Checked, topChk.Checked); err != nil {
			showSettingsError(err, w)
			return
		}
		w.Hide()
	})
	save.Importance = widget.HighImportance

	// ---- 浮窗显示（立即生效） ----
	miniSel := widget.NewRadioGroup([]string{"完整浮窗", "仅计时器"}, nil)
	miniSel.Horizontal, miniSel.Required = true, true
	if overlayMode == "mini" {
		miniSel.SetSelected("仅计时器")
	} else {
		miniSel.SetSelected("完整浮窗")
	}
	miniSel.OnChanged = func(label string) {
		if on := label == "仅计时器"; on != s.isMini() {
			showSettingsError(s.setMiniMode(on), w)
		}
	}
	s.settingsMini = miniSel
	bothChk := widget.NewCheck("左右两边都显示替身倒计时", nil)
	bothChk.SetChecked(showBoth)
	bothChk.OnChanged = func(on bool) { showSettingsError(s.setShowBothSides(on), w) }
	s.settingsBoth = bothChk
	// Topmost applies at once like the other display options.
	topChk.OnChanged = func(on bool) {
		s.mu.Lock()
		s.topmost, s.cfg.UI.AlwaysOnTop = on, on
		snapshot := s.settingsSnapshotLocked()
		s.mu.Unlock()
		err := snapshot.Save()
		s.applyFullTopmost()
		showSettingsError(err, w)
	}
	autoScaleChk := widget.NewCheck("仅计时器贴合文字（关闭后可拖动右边缘调长度、下边缘调高度和字号）", nil)
	autoScaleChk.SetChecked(miniAutoScale)
	autoScaleChk.OnChanged = func(on bool) {
		s.mu.Lock()
		s.cfg.UI.MiniAutoScale = on
		snapshot := s.settingsSnapshotLocked()
		s.mu.Unlock()
		err := snapshot.Save()
		s.applyMiniContentScale()
		showSettingsError(err, w)
	}

	// ---- 外观（拖动即预览，松手保存） ----
	opacityText := widget.NewLabel("")
	fontScaleText := widget.NewLabel("")
	miniOpacityText := widget.NewLabel("")
	appearanceNote := wrapped(appearanceError)
	opacitySlider := widget.NewSlider(minimumOverlayOpacity, maximumOverlayOpacity)
	opacitySlider.Step = 0.05
	opacitySlider.SetValue(opacity)
	fontScaleSlider := widget.NewSlider(minimumOverlayScale, maximumOverlayScale)
	fontScaleSlider.Step = 0.01
	fontScaleSlider.SetValue(fontScale)
	miniOpacitySlider := widget.NewSlider(minimumMiniOpacity, maximumMiniOpacity)
	miniOpacitySlider.Step = 0.05
	miniOpacitySlider.SetValue(miniOpacity)
	updateAppearanceLabels := func() {
		opacityText.SetText(fmt.Sprintf("完整浮窗不透明度：%.0f%%", opacitySlider.Value*100))
		fontScaleText.SetText(fmt.Sprintf("计时字号：%.0f%%", fontScaleSlider.Value*100))
		miniOpacityText.SetText(fmt.Sprintf("仅计时器不透明度：%.0f%%", miniOpacitySlider.Value*100))
	}
	previewAppearance := func() {
		if err := s.previewAppearance(opacitySlider.Value, fontScaleSlider.Value); err != nil {
			appearanceNote.SetText("未应用：" + err.Error())
			return
		}
		appearanceNote.SetText("")
	}
	opacitySlider.OnChanged = func(float64) { updateAppearanceLabels(); previewAppearance() }
	fontScaleSlider.OnChanged = func(float64) { updateAppearanceLabels(); previewAppearance() }
	commitAppearance := func(float64) {
		if err := s.previewAppearance(opacitySlider.Value, fontScaleSlider.Value); err != nil {
			dialog.ShowError(err, w)
			return
		}
		showSettingsError(s.saveAppearance(), w)
	}
	opacitySlider.OnChangeEnded = commitAppearance
	fontScaleSlider.OnChangeEnded = commitAppearance
	miniOpacitySlider.OnChanged = func(v float64) {
		updateAppearanceLabels()
		_ = s.setMiniOpacity(v, false)
	}
	miniOpacitySlider.OnChangeEnded = func(v float64) { showSettingsError(s.setMiniOpacity(v, true), w) }
	resetAppearance := widget.NewButton("恢复默认外观", func() {
		opacitySlider.SetValue(1)
		fontScaleSlider.SetValue(1)
		updateAppearanceLabels()
		previewAppearance()
		showSettingsError(s.saveAppearance(), w)
	})
	updateAppearanceLabels()

	calibrationEntry := widget.NewButtonWithIcon("区域校准（豆/头像）", theme.MediaPhotoIcon(), s.openCalibration)
	diagnosticEntry := widget.NewButtonWithIcon("采集与延迟诊断 / 原帧录制", theme.MediaRecordIcon(), s.openDiagnostics)
	general := container.NewVScroll(container.NewVBox(
		sectionCard("我方所在边", container.NewVBox(sideSel, rememberChk,
			wrapped("点选即生效。选的是我方，浮窗计时的是另一边；自动尚未认出时显示“待认边”。"))),
		sectionCard("名字与对面忍者", container.NewVBox(
			widget.NewLabel("我的名字"), nameEntry, textCheck, s.textStatusLabel, gpuChk,
			widget.NewLabel("指定对面忍者（留空自动识别）"), ninjaEntry,
			wrapped("照美冥［五代目水影］和半藏［秽土转生］同时显示 15 秒 / 10 秒。名字和忍者点下方按钮保存。"),
			save)),
		sectionCard("浮窗显示", container.NewVBox(miniSel, bothChk, topChk, autoScaleChk,
			wrapped("Ctrl+M 随时切换仅计时器；点击仅计时器展开设置、换边、两边计时和退出，拖动可移动。"))),
		sectionCard("外观", container.NewVBox(
			opacityText, opacitySlider, fontScaleText, fontScaleSlider, miniOpacityText, miniOpacitySlider,
			resetAppearance, appearanceNote)),
		sectionCard("区域校准", container.NewVBox(
			wrapped("少见分辨率识别不准时，在对局/训练场画面上自己框出豆和头像"), calibrationEntry)),
		sectionCard("诊断", container.NewVBox(
			wrapped("采集异常、延迟或识别问题时录制诊断，导出后反馈。"), diagnosticEntry)),
	))
	bg := canvas.NewRectangle(panelBG)
	if s.aboutCancel != nil {
		s.aboutCancel()
	}
	if s.captureCancel != nil {
		s.captureCancel()
	}
	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("常规", theme.SettingsIcon(), general),
		container.NewTabItemWithIcon("模拟器", theme.ComputerIcon(), s.captureControls(w)),
		container.NewTabItemWithIcon("关于与更新", theme.InfoIcon(), s.aboutControls(w)),
	)
	s.settingsTabs = tabs
	scanned := false
	tabs.OnSelected = func(tab *container.TabItem) {
		// Scan once per window: a rescan on every visit would re-highlight the
		// saved instance over the user's not-yet-applied choice.
		if tab.Text == "模拟器" && s.captureRefresh != nil && !scanned {
			scanned = true
			s.captureRefresh()
		}
	}
	tabs.SelectIndex(tab)
	w.SetContent(container.NewStack(bg, container.NewPadded(tabs)))
	w.Resize(fyne.NewSize(600, 660))
	w.Show()
	w.RequestFocus()
}

// syncSettingsControls mirrors changes made from the overlay, mini window or
// shortcuts into an open (or hidden) settings window.
func (s *session) syncSettingsControls() {
	s.syncSettingsSide()
	s.mu.Lock()
	mini, both := s.miniMode, s.cfg.UI.ShowBothSides
	s.mu.Unlock()
	if s.settingsMini != nil {
		label := "完整浮窗"
		if mini {
			label = "仅计时器"
		}
		if s.settingsMini.Selected != label {
			onChanged := s.settingsMini.OnChanged
			s.settingsMini.OnChanged = nil
			s.settingsMini.SetSelected(label)
			s.settingsMini.OnChanged = onChanged
		}
	}
	if s.settingsBoth != nil && s.settingsBoth.Checked != both {
		onChanged := s.settingsBoth.OnChanged
		s.settingsBoth.OnChanged = nil
		s.settingsBoth.SetChecked(both)
		s.settingsBoth.OnChanged = onChanged
	}
}

func (s *session) applySettings(nameText, ninjaText, sideText string, remember, top bool) error {
	names := splitNames(nameText)
	s.mu.Lock()
	if !slices.Equal(names, s.cfg.UI.PlayerNames) {
		s.clearAutoIdentity()
		// Clear even if a manual selection was active before switching back to auto.
		if sideMode(sideText) == "auto" {
			s.side = ""
		}
	}
	s.cfg.UI.PlayerNames = names
	s.cfg.UI.NinjaQuery = strings.TrimSpace(ninjaText)
	s.remember, s.cfg.UI.RememberSide = remember, remember
	topChanged := s.topmost != top
	s.topmost, s.cfg.UI.AlwaysOnTop = top, top
	s.myName = ""
	if len(names) > 0 {
		s.myName = names[0]
	}
	s.setSideModeLocked(sideMode(sideText))
	identity.SetMineNames(names)
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	err := snapshot.Save()
	if topChanged {
		s.applyFullTopmost()
	}
	s.syncSettingsSide()
	s.refreshOverlay()
	return err
}

func (s *session) swapSide() {
	s.mu.Lock()
	next := "left"
	switch s.side {
	case "left":
		next = "right"
	case "right":
		next = "auto"
	}
	s.mu.Unlock()
	if next == "auto" {
		showSettingsError(s.selectSideMode(next), s.dialogWindow())
		return
	}
	s.lockSide(next)
}

func (s *session) lockSide(side string) {
	if !manualSide(side) {
		return
	}
	showSettingsError(s.selectSideMode(side), s.dialogWindow())
}

func (s *session) pick(side string) { s.lockSide(side) }

func (s *session) setTextRecognition(on bool) error {
	s.mu.Lock()
	s.cfg.UI.AutoTextRecognition = on
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	err := snapshot.Save()
	if s.textControl != nil {
		s.textControl(on)
	}
	s.refreshOverlay()
	return err
}

// Caller holds s.mu. Text recognition is an optional aid, never a replacement
// for unknown/obscured bean states or the manually selected opponent variant.
func (s *session) textStatusText() string {
	if !s.cfg.UI.AutoTextRecognition {
		return "文字识别已关闭，使用模板和手动选择"
	}
	if s.textError != "" {
		return s.textError
	}
	switch s.textStatus {
	case "template":
		return "现有模板已识别，后台文字暂不需要补充"
	case "ready":
		return "已核对到文字；模板和手动选择优先"
	case "reading", "pending":
		return "后台核对文字中，采豆和倒计时不等待"
	case "unavailable":
		return "本地 OCR 暂不可用，特殊豆型模板和手动选择仍可用；请到诊断查看原因"
	default:
		return "等候清晰对局姓名；OCR 不上传截图"
	}
}

func (s *session) openAbout() {
	s.openSettings()
	if s.settingsTabs != nil {
		s.settingsTabs.SelectIndex(2)
	}
}
