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

// Serialize saves with state changes. Never let an old capture goroutine write
// a configuration snapshot over the user's more recent selection.
func (s *session) saveSettingsLocked() error {
	cfg := s.cfg
	cfg.UI.RememberSide = s.remember
	if !s.remember {
		cfg.UI.PlayerSide = "auto"
	}
	return config.Save(s.cfgPath, cfg)
}

func (s *session) selectSideMode(mode string) error {
	mode = sideMode(mode)
	s.mu.Lock()
	s.setSideModeLocked(mode)
	err := s.saveSettingsLocked()
	s.mu.Unlock()
	s.syncSettingsSide()
	s.refreshOverlay()
	return err
}

func (s *session) setRememberSide(remember bool) error {
	s.mu.Lock()
	s.remember, s.cfg.UI.RememberSide = remember, remember
	err := s.saveSettingsLocked()
	s.mu.Unlock()
	return err
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

func (s *session) openSettings() {
	w := s.settings
	if w == nil {
		w = fyne.CurrentApp().NewWindow("替身设置")
		s.settings = w
		w.SetOnClosed(func() {
			if s.captureCancel != nil {
				s.captureCancel()
			}
			if s.aboutCancel != nil {
				s.aboutCancel()
			}
			s.settings, s.settingsSide = nil, nil
			s.textStatusLabel = nil
			s.settingsTabs = nil
		})
	}
	// Rebuild from current state every time, not the stale controls from the last
	// time this window was hidden (the overlay's swap button may have changed it).
	s.mu.Lock()
	name := strings.Join(s.cfg.UI.PlayerNames, "，")
	ninjaText := s.cfg.UI.NinjaQuery
	mode, remember, top := s.cfg.UI.PlayerSide, s.remember, s.topmost
	autoText := s.cfg.UI.AutoTextRecognition
	autoUpdates := s.cfg.UI.AutoCheckUpdates
	opacity, fontScale := s.cfg.UI.WindowOpacity, s.cfg.UI.FontScale
	appearanceError := s.appearanceError
	textStatus := s.textStatusText()
	s.mu.Unlock()

	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("自己的账号名，用来自动认边")
	nameEntry.SetText(name)
	ninjaEntry := widget.NewSelectEntry([]string{"", ninja.FifthMizukage, ninja.Hashirama, ninja.Madara, ninja.Obito, ninja.Naruto})
	ninjaEntry.SetPlaceHolder("留空自动；或选择完整忍者版本")
	ninjaEntry.SetText(ninjaText)

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
	topChk := widget.NewCheck("窗口置顶", nil)
	topChk.SetChecked(top)
	textCheck := widget.NewCheck("后台识别名字（内置本地 OCR）", nil)
	textCheck.SetChecked(autoText)
	textCheck.OnChanged = func(on bool) { showSettingsError(s.setTextRecognition(on), w) }

	autoUpdateCheck := widget.NewCheck("自动检查软件更新", nil)
	autoUpdateCheck.SetChecked(autoUpdates)
	autoUpdateCheck.OnChanged = func(on bool) { showSettingsError(s.setAutoCheckUpdates(on), w) }
	updateHint := widget.NewLabel("启动约10秒后检查；之后最多每6小时检查一次。只显示提醒，不会自动下载、重启或打断对局。")
	updateHint.Wrapping = fyne.TextWrapWord

	opacityText := widget.NewLabel("")
	fontScaleText := widget.NewLabel("")
	appearanceNote := widget.NewLabel(appearanceError)
	appearanceNote.Wrapping = fyne.TextWrapWord
	opacitySlider := widget.NewSlider(minimumOverlayOpacity, maximumOverlayOpacity)
	opacitySlider.Step = 0.05
	opacitySlider.SetValue(opacity)
	fontScaleSlider := widget.NewSlider(minimumOverlayScale, maximumOverlayScale)
	fontScaleSlider.Step = 0.10
	fontScaleSlider.SetValue(fontScale)
	updateAppearanceLabels := func() {
		opacityText.SetText(fmt.Sprintf("窗口不透明度：%.0f%%", opacitySlider.Value*100))
		fontScaleText.SetText(fmt.Sprintf("计时浮窗字号：%.0f%%", fontScaleSlider.Value*100))
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
	resetAppearance := widget.NewButton("恢复默认外观", func() {
		opacitySlider.SetValue(1)
		fontScaleSlider.SetValue(1)
		updateAppearanceLabels()
		previewAppearance()
		showSettingsError(s.saveAppearance(), w)
	})
	updateAppearanceLabels()

	s.textStatusLabel = widget.NewLabel(textStatus)
	s.textStatusLabel.Wrapping = fyne.TextWrapWord
	hint := widget.NewLabel("认边方式点选即生效。选择的是我方，浮窗计时的是另一边；自动尚未认出时显示待认边。")
	hint.Wrapping = fyne.TextWrapWord
	save := widget.NewButton("保存", func() {
		if err := s.applySettings(nameEntry.Text, ninjaEntry.Text, sideSel.Selected, rememberChk.Checked, topChk.Checked); err != nil {
			showSettingsError(err, w)
			return
		}
		w.Hide()
	})
	save.Importance = widget.HighImportance
	form := container.NewVBox(
		widget.NewLabel("我方所在边"), sideSel, rememberChk, hint, widget.NewSeparator(),
		widget.NewLabel("我的名字"), nameEntry, textCheck, s.textStatusLabel,
		widget.NewLabel("指定对面忍者（留空自动识别）"), ninjaEntry,
		widget.NewLabel("仅照美冥［五代目水影］同时显示15秒/10秒"), topChk,
		widget.NewSeparator(), widget.NewLabel("外观与更新"),
		opacityText, opacitySlider, fontScaleText, fontScaleSlider, resetAppearance,
		widget.NewLabel("透明度只应用于计时浮窗；设置和更新窗口始终保持不透明。字号会立即预览并自动留出所需空间。"), appearanceNote,
		autoUpdateCheck, updateHint,
	)
	diagnosticEntry := widget.NewButton("采集与延迟诊断 / 原帧录制", s.openDiagnostics)
	bg := canvas.NewRectangle(panelBG)
	if s.aboutCancel != nil {
		s.aboutCancel()
	}
	if s.captureCancel != nil {
		s.captureCancel()
	}
	general := container.NewBorder(diagnosticEntry, save, nil, nil, container.NewVScroll(form))
	tabs := container.NewAppTabs(container.NewTabItem("常规", general), container.NewTabItem("模拟器", s.captureControls(w)), container.NewTabItem("关于与更新", s.aboutControls(w)))
	s.settingsTabs = tabs
	tabs.OnSelected = func(tab *container.TabItem) {
		if tab.Text == "模拟器" && s.captureRefresh != nil {
			s.captureRefresh()
		}
	}
	w.SetContent(container.NewStack(bg, container.NewPadded(tabs)))
	w.Resize(fyne.NewSize(620, 640))
	w.Show()
	w.RequestFocus()
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
	err := s.saveSettingsLocked()
	s.mu.Unlock()
	if topChanged {
		applyTopmost(overlayTitle, top)
	}
	s.syncSettingsSide()
	s.refreshOverlay()
	return err
}

func (s *session) swapSide() {
	s.mu.Lock()
	next := "right"
	if s.side == "right" {
		next = "left"
	}
	s.mu.Unlock()
	s.lockSide(next)
}

func (s *session) lockSide(side string) {
	if !manualSide(side) {
		return
	}
	showSettingsError(s.selectSideMode(side), s.win)
}

func (s *session) pick(side string) { s.lockSide(side) }

func (s *session) setTextRecognition(on bool) error {
	s.mu.Lock()
	s.cfg.UI.AutoTextRecognition = on
	err := s.saveSettingsLocked()
	s.mu.Unlock()
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
