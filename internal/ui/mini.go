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
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/win32"
)

const (
	minimumMiniOpacity = 0.20
	maximumMiniOpacity = 1.00
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
	m.s.detachedDragAnchor = nil
}

// miniButton keeps the controls up while the pointer rests on a button, even
// though the surface underneath has already received MouseOut.
type miniButton struct {
	widget.Button
	s        *session
	detached bool
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
	s.miniWin = a.NewWindow(overlayTitle + " · 迷你")
	s.miniWin.SetPadded(false)
	s.miniWin.SetFixedSize(true)
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
	s.miniStatus = canvas.NewText("替身计时·自动：等待决斗", tagIdle)
	s.miniStatus.TextSize = overlayTextSize(13, scale)
	s.miniStatus.Alignment = fyne.TextAlignCenter

	settings := newDetachedMiniButton(s, "设置", s.openSettings)
	swap := newDetachedMiniButton(s, "换边", s.swapSide)
	s.miniWindowBoth = newDetachedMiniButton(s, "两边计时", s.toggleBothSides)
	exit := newDetachedMiniButton(s, "退出", s.toggleMini)
	exit.Importance = widget.HighImportance
	buttons := container.NewGridWithColumns(4, settings, swap, s.miniWindowBoth, exit)
	s.miniWindowControls = container.NewStack(canvas.NewRectangle(miniControlsBG), buttons)
	s.miniWindowControls.Hide()

	s.miniWindowSurface = newMiniSurface(s)
	status := container.NewStack(canvas.NewRectangle(transparentColor), container.NewCenter(s.miniStatus), s.miniWindowSurface)
	s.miniRoot = container.NewVBox(status, s.miniWindowControls)
	return container.NewStack(canvas.NewRectangle(panelBG), container.NewCenter(s.miniRoot))
}

func (s *session) toggleDetachedMiniControls() {
	if s.miniWindowControls == nil {
		return
	}
	s.miniWindowExpanded = !s.miniWindowExpanded
	if s.miniWindowExpanded {
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

func (s *session) miniSideTextLocked() string {
	switch s.cfg.UI.PlayerSide {
	case "left":
		return "左"
	case "right":
		return "右"
	}
	switch s.side {
	case "left":
		return "自动·左"
	case "right":
		return "自动·右"
	default:
		return "自动"
	}
}

func miniTextValue(text string) string {
	if text == "" || text == "—" {
		return "等待决斗"
	}
	return text
}

func (s *session) miniTextLocked(primary, alternate, left, right string, dual bool) string {
	mode := s.miniSideTextLocked()
	if s.cfg.UI.ShowBothSides {
		return fmt.Sprintf("替身计时·%s：左 %s / 右 %s", mode, miniTextValue(left), miniTextValue(right))
	}
	primary = miniTextValue(primary)
	if dual {
		if alternate == "" {
			alternate = "—"
		}
		return fmt.Sprintf("替身计时·%s：15秒 %s / 10秒 %s", mode, primary, alternate)
	}
	return fmt.Sprintf("替身计时·%s：%s", mode, primary)
}

func (s *session) miniCollapsedText(primary string) string {
	s.mu.Lock()
	text := s.miniTextLocked(primary, "", "", "", false)
	s.mu.Unlock()
	return text
}

func (s *session) applyDetachedMiniClock(primary string, col color.Color) {
	s.applyDetachedMiniClockText(s.miniCollapsedText(primary), col)
}

func (s *session) applyDetachedMiniClockText(text string, col color.Color) {
	if s.miniStatus == nil {
		return
	}
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
	s.miniControls = container.NewStack(canvas.NewRectangle(miniControlsBG), grid)
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
	showSettingsError(s.setShowBothSides(on), s.win)
}

func (s *session) setShowBothSides(on bool) error {
	s.mu.Lock()
	s.cfg.UI.ShowBothSides = on
	err := s.saveSettingsLocked()
	s.mu.Unlock()
	s.applyBothSideVisibility()
	s.syncMiniControls()
	s.syncDetachedMiniControls()
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
	err := s.saveSettingsLocked()
	s.mu.Unlock()
	s.applyMiniVisibility()
	s.applyBothSideVisibility()
	if s.miniWin != nil {
		if on {
			s.win.Hide()
			s.applyMiniWindowState()
			s.miniWin.Show()
		} else {
			s.miniWin.Hide()
			s.win.Show()
			s.applyMiniWindowState()
		}
	} else {
		s.applyMiniWindowState()
	}
	s.syncDetachedMiniControls()
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
	var err error
	if save {
		err = s.saveSettingsLocked()
	}
	s.mu.Unlock()
	if mini {
		if nativeErr := s.nativeOverlayOpacity(opacity); nativeErr != nil {
			s.mu.Lock()
			s.appearanceError = "窗口透明度未应用：" + nativeErr.Error()
			s.mu.Unlock()
		}
	}
	return err
}

func (s *session) setMiniFloating(on bool) error {
	s.mu.Lock()
	s.cfg.UI.MiniFloating = on
	err := s.saveSettingsLocked()
	s.mu.Unlock()
	s.applyMiniWindowState()
	return err
}

func (s *session) fitDetachedMiniWindow() {
	if s.miniWin == nil || s.miniRoot == nil {
		return
	}
	s.mu.Lock()
	width, height := s.cfg.UI.MiniWidth, s.cfg.UI.MiniHeight
	s.mu.Unlock()
	minimum := fyne.NewSize(float32(max(260, width)), float32(max(48, height)))
	s.miniWin.Resize(minimum.Max(s.miniRoot.MinSize()))
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
