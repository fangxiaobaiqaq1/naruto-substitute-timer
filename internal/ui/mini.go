package ui

import (
	"errors"
	"fmt"
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

func (m *miniSurface) MouseIn(*desktop.MouseEvent)    { m.s.miniHover(&m.s.miniSurfaceHover, true) }
func (m *miniSurface) MouseMoved(*desktop.MouseEvent) {}
func (m *miniSurface) MouseOut()                      { m.s.miniHover(&m.s.miniSurfaceHover, false) }
func (m *miniSurface) Dragged(ev *fyne.DragEvent)     { m.s.dragMini(ev.Dragged) }
func (m *miniSurface) DragEnd()                       { m.s.dragAnchor = nil }

// miniButton keeps the controls up while the pointer rests on a button, even
// though the surface underneath has already received MouseOut.
type miniButton struct {
	widget.Button
	s *session
}

func newMiniButton(s *session, label string, tapped func()) *miniButton {
	b := &miniButton{s: s}
	b.Text, b.OnTapped = label, tapped
	b.Importance = widget.LowImportance
	b.ExtendBaseWidget(b)
	return b
}

func (b *miniButton) MouseIn(ev *desktop.MouseEvent) {
	b.Button.MouseIn(ev)
	b.s.miniHover(&b.s.miniButtonHover, true)
}

func (b *miniButton) MouseOut() {
	b.Button.MouseOut()
	b.s.miniHover(&b.s.miniButtonHover, false)
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
	s.applyMiniWindowState()
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
	floating := s.miniMode && s.cfg.UI.MiniFloating
	top := s.topmost
	s.mu.Unlock()
	var errs []error
	if err := s.nativeOverlayOpacity(opacity); err != nil && opacity < 1 {
		errs = append(errs, fmt.Errorf("窗口透明度未应用：%w", err))
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

func (s *session) nativeFloating(on, topmost bool) error {
	if s.win == nil {
		return nil
	}
	if s.floatingHook != nil {
		return s.floatingHook(on)
	}
	return s.runNativeHWND(func(hwnd uintptr) error {
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

// miniDragAnchor records where a native drag started, in screen pixels.
type miniDragAnchor struct {
	cursorX, cursorY, windowX, windowY int32
}

// runNativeHWND always targets this session's own window, never a title match.
func (s *session) runNativeHWND(f func(uintptr) error) error {
	native, ok := s.win.(driver.NativeWindow)
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
