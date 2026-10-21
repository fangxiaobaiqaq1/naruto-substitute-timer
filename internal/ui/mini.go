package ui

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/config"
	"narutotimer/internal/win32"
)

const (
	minimumMiniOpacity = 0.20
	maximumMiniOpacity = 1.00
	// Timer-only text sizes at 100 %: the 我方 caption above the clock, as
	// on the Android mini card.
	miniCaptionText, miniClockText = 11, 22
	// Clock padding inside the timer-only pill.
	miniPadY, miniPadX = 3, 14
	// miniEdgeSize is the grab strip on the pill's right and bottom edges.
	miniEdgeSize = 6
	// miniGrabMargin is a keyed-out (click-through) band past the pill that
	// still belongs to the strips. Fyne starts a drag only if the pointer's
	// previous position is on the strip, and drags under its 2-unit threshold
	// would otherwise leave the window first when begun on the pill's edge.
	miniGrabMargin = 3
	// Clock scale range when the user drags the pill's height.
	minimumMiniScale, maximumMiniScale = 0.5, 4.0
	// Moving from the timer surface onto a hover button fires MouseOut before
	// the button's MouseIn; the hide check runs after that hand-over.
	miniHoverSettle = 80 * time.Millisecond
)

// miniSurface covers the mini timer. It reports hover so the controls can
// appear, and in floating mode turns drags into native window moves.
type miniSurface struct {
	widget.BaseWidget
	s *session
}

func newMiniSurface(s *session) *miniSurface {
	m := &miniSurface{s: s}
	m.ExtendBaseWidget(m)
	return m
}

func (m *miniSurface) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(transparentColor))
}

func (m *miniSurface) MouseIn(*desktop.MouseEvent) {
	if m.s.miniWindowSurface == m {
		return
	}
	m.s.miniHover(&m.s.miniSurfaceHover, true)
}
func (m *miniSurface) MouseMoved(*desktop.MouseEvent) {}
func (m *miniSurface) MouseOut() {
	if m.s.miniWindowSurface == m {
		return
	}
	m.s.miniHover(&m.s.miniSurfaceHover, false)
}
func (m *miniSurface) Tapped(*fyne.PointEvent) {
	if m.s.miniWindowSurface == m {
		m.s.toggleDetachedMiniControls()
	}
}
func (m *miniSurface) Dragged(ev *fyne.DragEvent) {
	if m.s.miniWindowSurface == m {
		m.s.dragDetachedMini(ev.Dragged)
		return
	}
	m.s.dragMini(ev.Dragged)
}
func (m *miniSurface) DragEnd() {
	m.s.dragAnchor = nil
	if m.s.detachedDragAnchor != nil {
		m.s.detachedDragAnchor = nil
		m.s.rememberMiniPosition()
	}
}

// miniEdge stretches the timer-only pill from its right edge (width) or
// bottom edge (height). The window has no native sizing frame: Windows draws
// that frame around the whole window, larger than the pill.
type miniEdge struct {
	widget.BaseWidget
	s        *session
	vertical bool
}

func newMiniEdge(s *session, vertical bool) *miniEdge {
	e := &miniEdge{s: s, vertical: vertical}
	e.ExtendBaseWidget(e)
	return e
}

func (e *miniEdge) CreateRenderer() fyne.WidgetRenderer {
	r := canvas.NewRectangle(transparentColor)
	r.SetMinSize(fyne.NewSize(miniEdgeSize+miniGrabMargin, miniEdgeSize+miniGrabMargin))
	return widget.NewSimpleRenderer(r)
}

func (e *miniEdge) Cursor() desktop.Cursor {
	if e.vertical {
		return desktop.VResizeCursor
	}
	return desktop.HResizeCursor
}

// Dragged puts the edge under the pointer. Canvas coordinates suffice: a
// right/bottom resize never moves the window's top-left corner.
func (e *miniEdge) Dragged(ev *fyne.DragEvent) {
	e.s.resizeDetachedMini(ev.AbsolutePosition, e.vertical)
}

func (e *miniEdge) DragEnd() { e.s.rememberMiniSize() }

// miniButton keeps the controls up while the pointer rests on a button, even
// though the surface underneath has already received MouseOut.
type miniButton struct {
	widget.Button
	s        *session
	detached bool
}

type scaledMiniButton struct {
	widget.BaseWidget
	s          *session
	Text       string
	OnTapped   func()
	high       bool
	hovered    bool
	scale      float32
	label      *canvas.Text
	background *canvas.Rectangle
}

func newScaledMiniButton(s *session, label string, tapped func(), high bool) *scaledMiniButton {
	b := &scaledMiniButton{s: s, Text: label, OnTapped: tapped, high: high, scale: 1}
	b.label = canvas.NewText(label, tagMine)
	b.label.Alignment = fyne.TextAlignCenter
	b.background = canvas.NewRectangle(panelBtn)
	b.ExtendBaseWidget(b)
	b.setScale(1)
	return b
}

func (b *scaledMiniButton) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(b.background, container.NewCenter(b.label)))
}

func (b *scaledMiniButton) setScale(scale float32) {
	if scale <= 0 {
		scale = 1
	}
	b.scale = scale
	b.label.TextSize = b.s.miniTextSize(13 * scale)
	b.label.Refresh()
	// A real hit target: the label plus padding, never just the glyphs.
	text := fyne.MeasureText(b.Text, b.label.TextSize, b.label.TextStyle)
	b.background.SetMinSize(fyne.NewSize(text.Width+20*scale, text.Height+12*scale))
	b.background.CornerRadius = 8 * scale
	b.refreshColors()
}

// Colours never use glassBG: that is the mini window's transparency key and
// would punch holes (and click-through spots) into the controls.
func (b *scaledMiniButton) refreshColors() {
	switch {
	case b.hovered:
		b.background.FillColor = panelHover
		b.label.Color = tagMine
	case b.high:
		b.background.FillColor = exitBG
		b.label.Color = exitText
	default:
		b.background.FillColor = panelBtn
		b.label.Color = tagMine
	}
	b.background.Refresh()
	b.label.Refresh()
}

func (b *scaledMiniButton) MouseIn(*desktop.MouseEvent) {
	b.hovered = true
	b.refreshColors()
}

func (b *scaledMiniButton) MouseOut() {
	b.hovered = false
	b.refreshColors()
}

func (b *scaledMiniButton) Tapped(*fyne.PointEvent) {
	if b.OnTapped != nil {
		b.OnTapped()
	}
}

func (b *scaledMiniButton) SetText(text string) {
	b.Text = text
	b.label.Text = text
	b.setScale(b.scale)
	b.Refresh()
}

func (s *session) miniTextSize(base float32) float32 {
	s.mu.Lock()
	fontScale := s.cfg.UI.FontScale
	s.mu.Unlock()
	return overlayTextSize(base, fontScale)
}

func newMiniButton(s *session, label string, tapped func()) *miniButton {
	b := &miniButton{s: s}
	b.Text, b.OnTapped = label, tapped
	b.Importance = widget.LowImportance
	b.ExtendBaseWidget(b)
	return b
}

func newDetachedMiniButton(s *session, label string, tapped func()) *miniButton {
	b := newMiniButton(s, label, tapped)
	b.detached = true
	return b
}

func (b *miniButton) MouseIn(ev *desktop.MouseEvent) {
	b.Button.MouseIn(ev)
	if !b.detached {
		b.s.miniHover(&b.s.miniButtonHover, true)
	}
}

func (b *miniButton) MouseOut() {
	b.Button.MouseOut()
	if !b.detached {
		b.s.miniHover(&b.s.miniButtonHover, false)
	}
}

func (s *session) setupDetachedMiniWindow(a fyne.App) {
	if a == nil {
		return
	}
	if driver, ok := a.Driver().(desktop.Driver); ok {
		s.miniWin = driver.CreateSplashWindow()
		// Splash windows are untitled ("Fyne Application" in Alt-Tab).
		s.miniWin.SetTitle(overlayTitle + " · 迷你")
	} else {
		s.miniWin = a.NewWindow(overlayTitle + " · 迷你")
	}
	s.miniWin.SetPadded(false)
	s.miniWin.SetFixedSize(false)
	s.miniWin.SetContent(s.buildDetachedMiniContent())
	s.miniWin.SetCloseIntercept(func() {
		if s.isMini() {
			showSettingsError(s.setMiniMode(false), s.win)
			return
		}
		s.miniWin.Hide()
	})
	s.fitDetachedMiniWindow()
}

func (s *session) buildDetachedMiniContent() fyne.CanvasObject {
	s.mu.Lock()
	scale := s.cfg.UI.FontScale
	s.mu.Unlock()
	s.miniCaption = canvas.NewText("我方·自动·待认边", tagIdle)
	s.miniCaption.TextSize = overlayTextSize(miniCaptionText, scale)
	s.miniCaption.Alignment = fyne.TextAlignCenter
	s.miniStatus = canvas.NewText("待机", tagIdle)
	s.miniStatus.TextSize = overlayTextSize(miniClockText, scale)
	s.miniStatus.TextStyle = fyne.TextStyle{Bold: true}
	s.miniStatus.Alignment = fyne.TextAlignCenter

	settings := newScaledMiniButton(s, "设置", s.openSettings, false)
	swap := newScaledMiniButton(s, "换边", s.swapSide, false)
	s.miniWindowBoth = newScaledMiniButton(s, "两边计时", s.toggleBothSides, false)
	// It returns to the full overlay; quitting is the full window's close button.
	exit := newScaledMiniButton(s, "退出迷你", s.toggleMini, true)
	s.miniWindowButtons = []*scaledMiniButton{settings, swap, s.miniWindowBoth, exit}
	buttons := container.NewGridWithColumns(4, settings, swap, s.miniWindowBoth, exit)
	controlsBG := canvas.NewRectangle(panelBG)
	controlsBG.CornerRadius = 10
	s.miniWindowControls = container.NewStack(controlsBG, container.NewPadded(buttons))
	s.miniWindowControls.Hide()

	// An opaque pill (not the key colour) behind the clock: readable over any
	// game background, and the whole pill is clickable/draggable, not only the
	// glyph strokes. The window is exactly the pill (plus the open action
	// row); only the rounded corners show the keyed-out canvas colour.
	s.miniPill = canvas.NewRectangle(panelBG)
	s.miniPill.CornerRadius = 12
	s.miniPill.StrokeColor = panelBorder
	s.miniPill.StrokeWidth = 1
	s.miniWindowSurface = newMiniSurface(s)
	clock := container.New(layout.NewCustomPaddedLayout(miniPadY, miniPadY, miniPadX, miniPadX),
		container.NewCenter(container.New(layout.NewCustomPaddedVBoxLayout(0), s.miniCaption, s.miniStatus)))
	s.miniPillBox = container.NewStack(s.miniPill, clock, s.miniWindowSurface)
	s.miniEdges = container.NewStack(
		container.NewHBox(layout.NewSpacer(), newMiniEdge(s, false)),
		container.NewVBox(layout.NewSpacer(), newMiniEdge(s, true)))
	pill := container.New(layout.NewCustomPaddedLayout(0, miniGrabMargin, 0, miniGrabMargin), s.miniPillBox)
	s.miniRoot = container.NewBorder(nil, s.miniWindowControls, nil, nil, container.NewStack(pill, s.miniEdges))
	return s.miniRoot
}

func (s *session) toggleDetachedMiniControls() {
	if s.miniWindowControls == nil {
		return
	}
	s.setDetachedMiniExpanded(!s.miniWindowExpanded)
}

func (s *session) setDetachedMiniExpanded(on bool) {
	if s.miniWindowControls == nil || s.miniWindowExpanded == on {
		return
	}
	s.miniWindowExpanded = on
	if on {
		s.miniWindowControls.Show()
	} else {
		s.miniWindowControls.Hide()
	}
	s.miniWindowControls.Refresh()
	s.fitDetachedMiniWindow()
}

func (s *session) syncDetachedMiniControls() {
	if s.miniWindowBoth == nil {
		return
	}
	s.mu.Lock()
	both := s.cfg.UI.ShowBothSides
	s.mu.Unlock()
	label := "两边计时"
	if both {
		label = "单边计时"
	}
	if s.miniWindowBoth.Text != label {
		s.miniWindowBoth.SetText(label)
	}
}

func (s *session) applyMiniContentScale() {
	if s.miniWin == nil || s.miniRoot == nil {
		return
	}
	// Font-size previews must reach the mini at once, even when its adaptive
	// scale itself did not change.
	s.setMiniContentScale(s.miniAdaptiveScale)
	s.fitDetachedMiniWindow()
}

func (s *session) setMiniContentScale(scale float32) {
	if scale <= 0 {
		scale = 1
	}
	s.miniAdaptiveScale = scale
	if s.miniStatus != nil {
		s.miniStatus.TextSize = s.miniTextSize(miniClockText * scale)
		s.miniStatus.Refresh()
		s.miniCaption.TextSize = s.miniTextSize(miniCaptionText * scale)
		s.miniCaption.Refresh()
	}
	for _, button := range s.miniWindowButtons {
		button.setScale(scale)
	}
	if s.miniRoot != nil {
		s.miniRoot.Refresh()
	}
}

// miniSideTextLocked is the player-side caption of the Android cards.
func (s *session) miniSideTextLocked() string {
	switch s.cfg.UI.PlayerSide {
	case "left":
		return "我方·左"
	case "right":
		return "我方·右"
	}
	switch s.side {
	case "left":
		return "我方·自动·左"
	case "right":
		return "我方·自动·右"
	}
	return "我方·自动·待认边"
}

// miniWaitingLocked says why no countdown runs, with the Android card's words.
func (s *session) miniWaitingLocked() string {
	switch {
	case s.captureLost:
		return "待采集"
	case s.hold:
		return "待识别"
	case !s.fighting:
		return "待机"
	case s.side == "":
		return "待认边"
	}
	opponentLeft := s.side == "right"
	if !sideObservable(s.beads, opponentLeft) || len(sideBeads(s.beads, opponentLeft)) != s.expectedSlots(opponentLeft) {
		return "待识别"
	}
	return "待替身"
}

func miniBothSideValue(text string) string {
	if text == "" || text == "—" {
		return "—"
	}
	return text
}

func (s *session) miniTextLocked(primary, alternate, left, right string, dual bool) string {
	if s.cfg.UI.ShowBothSides {
		return fmt.Sprintf("左%s 右%s", miniBothSideValue(left), miniBothSideValue(right))
	}
	if primary == "" || primary == "—" {
		primary = s.miniWaitingLocked()
	}
	if dual {
		if alternate == "" {
			alternate = "—"
		}
		return fmt.Sprintf("%s / %s", primary, alternate)
	}
	return primary
}

func (s *session) applyDetachedMiniClock(primary string, col color.Color) {
	s.mu.Lock()
	caption, text := s.miniSideTextLocked(), s.miniTextLocked(primary, "", "", "", false)
	s.mu.Unlock()
	s.applyDetachedMiniClockText(caption, text, col)
}

func (s *session) applyDetachedMiniClockText(caption, text string, col color.Color) {
	if s.miniStatus == nil {
		return
	}
	s.miniCaption.Text = caption
	s.miniCaption.Refresh()
	s.miniStatus.Text = text
	s.miniStatus.Color = col
	s.miniStatus.Refresh()
	if s.miniRoot != nil {
		s.miniRoot.Refresh()
	}
	s.fitDetachedMiniWindow()
}

func (s *session) buildMiniControls() fyne.CanvasObject {
	settings := newMiniButton(s, "设置", s.openSettings)
	swap := newMiniButton(s, "换边", s.swapSide)
	s.miniBothButton = newMiniButton(s, "两边计时", s.toggleBothSides)
	exit := newMiniButton(s, "退出迷你", s.toggleMini)
	exit.Importance = widget.HighImportance
	grid := container.NewGridWithColumns(2, settings, swap, s.miniBothButton, exit)
	s.miniControls = container.NewStack(canvas.NewRectangle(panelBG), grid)
	s.miniControls.Hide()
	// Reserve the controls' size so showing them never resizes the window.
	spacer := canvas.NewRectangle(transparentColor)
	spacer.SetMinSize(s.miniControls.MinSize())
	s.miniSurface = newMiniSurface(s)
	s.miniLayer = container.NewStack(spacer, s.miniSurface, container.NewCenter(s.miniControls))
	return s.miniLayer
}

func (s *session) isMini() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.miniMode
}

func (s *session) miniHover(flag *bool, on bool) {
	*flag = on
	if on {
		s.syncMiniControls()
		return
	}
	if s.hoverSettleHook != nil {
		s.hoverSettleHook(s.syncMiniControls)
		return
	}
	time.AfterFunc(miniHoverSettle, func() {
		if !s.stopped() {
			fyne.Do(s.syncMiniControls)
		}
	})
}

// syncMiniControls runs on the UI goroutine.
func (s *session) syncMiniControls() {
	if s.miniControls == nil {
		return
	}
	show := s.isMini() && (s.miniSurfaceHover || s.miniButtonHover)
	if s.miniBothButton != nil {
		s.mu.Lock()
		both := s.cfg.UI.ShowBothSides
		s.mu.Unlock()
		label := "两边计时"
		if both {
			label = "单边计时"
		}
		if s.miniBothButton.Text != label {
			s.miniBothButton.SetText(label)
		}
	}
	if show == s.miniControls.Visible() {
		return
	}
	if show {
		s.miniControls.Show()
	} else {
		s.miniControls.Hide()
	}
	s.miniControls.Refresh()
}

func (s *session) toggleBothSides() {
	s.mu.Lock()
	on := !s.cfg.UI.ShowBothSides
	s.mu.Unlock()
	showSettingsError(s.setShowBothSides(on), s.dialogWindow())
}

func (s *session) setShowBothSides(on bool) error {
	s.mu.Lock()
	s.cfg.UI.ShowBothSides = on
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	err := snapshot.Save()
	s.applyBothSideVisibility()
	s.syncMiniControls()
	s.syncDetachedMiniControls()
	s.syncSettingsControls()
	s.refreshClock()
	return err
}

// setMiniMode switches layout, opacity and (optionally) the native frame.
func (s *session) setMiniMode(on bool) error {
	s.mu.Lock()
	s.miniMode = on
	if on {
		s.cfg.UI.OverlayMode = "mini"
	} else {
		s.cfg.UI.OverlayMode = "full"
	}
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	err := snapshot.Save()
	s.applyMiniVisibility()
	s.applyBothSideVisibility()
	if s.miniWin != nil {
		if on {
			s.rememberWindowPosition()
			s.setDetachedMiniExpanded(false)
			s.win.Hide()
			// Show first: an never-shown window has no HWND for the native
			// colour key, opacity, frame and topmost styles.
			s.miniWin.Show()
			s.restoreMiniPosition()
			s.applyMiniWindowState()
		} else {
			s.rememberMiniPosition()
			s.miniWin.Hide()
			s.win.Show()
			s.restoreWindowPosition()
			s.applyMiniWindowState()
			s.applyFullTopmost()
		}
	} else {
		s.applyMiniWindowState()
	}
	s.syncDetachedMiniControls()
	s.syncSettingsControls()
	s.refreshClock()
	return err
}

// effectiveOpacity is the opacity the timer window should currently have.
func (s *session) effectiveOpacityLocked() float64 {
	if s.miniMode {
		return s.cfg.UI.MiniOpacity
	}
	return s.cfg.UI.WindowOpacity
}

// applyMiniWindowState applies opacity, frame and z-order for the current
// mode. Native failures are reported in the settings page, never fatal.
func (s *session) applyMiniWindowState() {
	s.mu.Lock()
	opacity := s.effectiveOpacityLocked()
	mini := s.miniMode
	floating := mini && (s.cfg.UI.MiniFloating || s.miniWin != nil)
	top := s.topmost
	s.mu.Unlock()
	var errs []error
	if err := s.nativeOverlayOpacity(opacity); err != nil && opacity < 1 {
		errs = append(errs, fmt.Errorf("窗口透明度未应用：%w", err))
	}
	if !mini && s.miniWin != nil && s.floatingApplied {
		if err := s.nativeFloating(false, top); err != nil {
			errs = append(errs, fmt.Errorf("迷你窗口样式未恢复：%w", err))
		} else {
			s.floatingApplied = false
		}
	}
	if floating != s.floatingApplied {
		if err := s.nativeFloating(floating, top); err != nil {
			errs = append(errs, fmt.Errorf("悬浮窗样式未应用：%w", err))
		} else {
			s.floatingApplied = floating
		}
	}
	if mini {
		// Reapply the key after SetWindowOpacity: LWA_ALPHA otherwise replaces
		// the color-key flag on Windows.
		_ = s.nativeMiniColorKey(opacity)
	}
	s.mu.Lock()
	s.appearanceError = ""
	if len(errs) > 0 {
		s.appearanceError = errors.Join(errs...).Error()
	}
	s.mu.Unlock()
	if s.overlay != nil {
		s.fitOverlayWindow()
	}
	if s.miniWin != nil {
		s.fitDetachedMiniWindow()
	}
}

func (s *session) setMiniOpacity(opacity float64, save bool) error {
	if opacity < minimumMiniOpacity || opacity > maximumMiniOpacity {
		return fmt.Errorf("迷你窗口不透明度必须在 %.0f%% 到 %.0f%% 之间", minimumMiniOpacity*100, maximumMiniOpacity*100)
	}
	s.mu.Lock()
	s.cfg.UI.MiniOpacity = opacity
	mini := s.miniMode
	var snapshot config.Snapshot
	if save {
		snapshot = s.settingsSnapshotLocked()
	}
	s.mu.Unlock()
	var err error
	if save {
		err = snapshot.Save()
	}
	if mini {
		if nativeErr := s.nativeOverlayOpacity(opacity); nativeErr != nil {
			s.mu.Lock()
			s.appearanceError = "窗口透明度未应用：" + nativeErr.Error()
			s.mu.Unlock()
		}
		_ = s.nativeMiniColorKey(opacity)
	}
	return err
}

func (s *session) setMiniFloating(on bool) error {
	s.mu.Lock()
	s.cfg.UI.MiniFloating = on
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	err := snapshot.Save()
	s.applyMiniWindowState()
	return err
}

// fitDetachedMiniWindow sizes the window to the pill (plus the open action
// row and the thin click-through grab margin). A dragged pill size is kept,
// never below the text; its height sets the clock scale.
func (s *session) fitDetachedMiniWindow() {
	if s.miniWin == nil || s.miniRoot == nil {
		return
	}
	s.mu.Lock()
	want, auto := s.cfg.UI.MiniSize, s.cfg.UI.MiniAutoScale
	s.mu.Unlock()
	pill, scale := fyne.Size{}, float32(1)
	if !auto && want != nil && want.Width > 0 && want.Height > 0 {
		pill = fyne.NewSize(want.Width, want.Height)
		scale = float32(math.Min(maximumMiniScale, math.Max(minimumMiniScale, float64(pill.Height/s.miniNaturalHeight()))))
	}
	if auto {
		s.miniEdges.Hide()
	} else {
		s.miniEdges.Show()
	}
	if math.Abs(float64(scale-s.miniAdaptiveScale)) > 0.01 {
		s.setMiniContentScale(scale)
	}
	// Never narrower than any countdown, so 10.0 -> 9.9 does not jump.
	clock := fyne.MeasureText("88.8", s.miniStatus.TextSize, s.miniStatus.TextStyle)
	s.miniPill.SetMinSize(pill.Max(fyne.NewSize(clock.Width+2*miniPadX, 0)))
	if size := s.miniRoot.MinSize(); size != s.miniWin.Canvas().Size() {
		s.miniWin.Resize(size)
	}
}

// miniNaturalHeight is the pill height at 100 %: caption, clock and padding.
func (s *session) miniNaturalHeight() float32 {
	return fyne.MeasureText("我", s.miniTextSize(miniCaptionText), fyne.TextStyle{}).Height +
		fyne.MeasureText("8", s.miniTextSize(miniClockText), fyne.TextStyle{Bold: true}).Height + 2*miniPadY
}

// miniShownSize is the collapsed pill as drawn: the dragged size, never below
// the text.
func (s *session) miniShownSize() config.ScreenSize {
	shown := s.miniPillBox.MinSize()
	return config.ScreenSize{Width: shown.Width, Height: shown.Height}
}

// resizeDetachedMini puts the dragged edge under the pointer; the other
// dimension keeps the stored size, or the size shown now.
func (s *session) resizeDetachedMini(at fyne.Position, vertical bool) {
	if s.miniRoot == nil {
		return
	}
	size := s.miniShownSize()
	s.mu.Lock()
	if s.cfg.UI.MiniSize != nil {
		size = *s.cfg.UI.MiniSize
	}
	if vertical {
		size.Height = at.Y + miniEdgeSize/2
	} else {
		size.Width = at.X + miniEdgeSize/2
	}
	s.cfg.UI.MiniSize = &size
	s.mu.Unlock()
	s.fitDetachedMiniWindow()
}

// rememberMiniSize saves the size as shown, so the next drag starts at the
// visible edge even if this one went below the text.
func (s *session) rememberMiniSize() {
	if s.miniRoot == nil {
		return
	}
	size := s.miniShownSize()
	s.mu.Lock()
	if s.cfg.UI.MiniSize != nil {
		s.cfg.UI.MiniSize = &size
	}
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	err := snapshot.Save()
	if err != nil {
		s.debugf("save mini size: %v", err)
	}
}

func (s *session) nativeMiniColorKey(opacity float64) error {
	if s.miniWin == nil {
		return nil
	}
	return s.runNativeWindowHWND(s.miniWin, func(hwnd uintptr) error {
		return win32.SetWindowColorKey(hwnd, glassBG, opacity)
	})
}

func (s *session) nativeFloating(on, topmost bool) error {
	target := s.win
	if s.miniWin != nil && (s.miniMode || s.floatingApplied) {
		target = s.miniWin
	}
	if target == nil {
		return nil
	}
	if s.floatingHook != nil {
		return s.floatingHook(on)
	}
	return s.runNativeWindowHWND(target, func(hwnd uintptr) error {
		if target == s.miniWin {
			// Already a frameless splash window, resized by its own edges. A
			// native sizing frame is drawn around the whole keyed window, and
			// writing a saved style back re-showed it after Hide.
			return win32.SetWindowTopmost(hwnd, on || topmost)
		}
		previous, err := win32.SetWindowFrameless(hwnd, on, s.savedStyle)
		if err != nil {
			return err
		}
		if on {
			s.savedStyle = previous
			return win32.SetWindowTopmost(hwnd, true)
		}
		s.savedStyle = 0
		return win32.SetWindowTopmost(hwnd, topmost)
	})
}

func (s *session) dragMini(delta fyne.Delta) {
	s.mu.Lock()
	floating := s.miniMode && s.cfg.UI.MiniFloating
	s.mu.Unlock()
	if !floating || s.win == nil {
		return
	}
	scale := s.win.Canvas().Scale()
	dx, dy := int32(math.Round(float64(delta.DX*scale))), int32(math.Round(float64(delta.DY*scale)))
	if dx == 0 && dy == 0 {
		return
	}
	if s.moveHook != nil {
		s.moveHook(dx, dy)
		return
	}
	// Fyne deltas are window-relative and every move shifts the window under
	// the pointer, so the native move follows the screen cursor from an anchor.
	_ = s.runNativeHWND(func(hwnd uintptr) error {
		cx, cy, err := win32.CursorPos()
		if err != nil {
			return err
		}
		if s.dragAnchor == nil {
			wx, wy, err := win32.WindowPos(hwnd)
			if err != nil {
				return err
			}
			s.dragAnchor = &miniDragAnchor{cursorX: cx, cursorY: cy, windowX: wx, windowY: wy}
			return nil
		}
		a := s.dragAnchor
		return win32.MoveWindowTo(hwnd, a.windowX+cx-a.cursorX, a.windowY+cy-a.cursorY)
	})
}

func (s *session) dragDetachedMini(delta fyne.Delta) {
	if s.miniWin == nil {
		return
	}
	scale := s.miniWin.Canvas().Scale()
	dx, dy := int32(math.Round(float64(delta.DX*scale))), int32(math.Round(float64(delta.DY*scale)))
	if dx == 0 && dy == 0 {
		return
	}
	if s.moveHook != nil {
		s.moveHook(dx, dy)
		return
	}
	_ = s.runNativeWindowHWND(s.miniWin, func(hwnd uintptr) error {
		cx, cy, err := win32.CursorPos()
		if err != nil {
			return err
		}
		if s.detachedDragAnchor == nil {
			wx, wy, err := win32.WindowPos(hwnd)
			if err != nil {
				return err
			}
			s.detachedDragAnchor = &miniDragAnchor{cursorX: cx, cursorY: cy, windowX: wx, windowY: wy}
			return nil
		}
		a := s.detachedDragAnchor
		return win32.MoveWindowTo(hwnd, a.windowX+cx-a.cursorX, a.windowY+cy-a.cursorY)
	})
}

// miniDragAnchor records where a native drag started, in screen pixels.
type miniDragAnchor struct {
	cursorX, cursorY, windowX, windowY int32
}

// runNativeHWND always targets this session's own window, never a title match.
func (s *session) runNativeHWND(f func(uintptr) error) error {
	return s.runNativeWindowHWND(s.win, f)
}

func (s *session) runNativeWindowHWND(w fyne.Window, f func(uintptr) error) error {
	native, ok := w.(driver.NativeWindow)
	if !ok {
		return errors.New("当前窗口后端不支持原生窗口样式")
	}
	var result error
	native.RunNative(func(context any) {
		ctx, ok := context.(driver.WindowsWindowContext)
		if !ok || ctx.HWND == 0 {
			result = errors.New("无法获取计时浮窗句柄")
			return
		}
		result = f(ctx.HWND)
	})
	return result
}
