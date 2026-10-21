package ui

import (
	"errors"
	"fmt"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver"

	"narutotimer/internal/config"
	"narutotimer/internal/win32"
)

const (
	minimumOverlayOpacity = 0.40
	maximumOverlayOpacity = 1.00
	minimumOverlayScale   = 0.25
	maximumOverlayScale   = 3.00
)

func validOverlayAppearance(opacity, scale float64) error {
	if opacity < minimumOverlayOpacity || opacity > maximumOverlayOpacity {
		return fmt.Errorf("窗口透明度必须在 %.0f%% 到 %.0f%% 之间", minimumOverlayOpacity*100, maximumOverlayOpacity*100)
	}
	if scale < minimumOverlayScale || scale > maximumOverlayScale {
		return fmt.Errorf("字体大小必须在 %.0f%% 到 %.0f%% 之间", minimumOverlayScale*100, maximumOverlayScale*100)
	}
	return nil
}

func normalizedOverlayScale(scale float64) float64 {
	if scale < minimumOverlayScale || scale > maximumOverlayScale {
		return 1
	}
	return scale
}

func overlayTextSize(base float32, scale float64) float32 {
	return float32(math.Round(float64(base)*normalizedOverlayScale(scale)*10) / 10)
}

// setFyneWindowOpacity runs against the exact native Fyne window. The test
// driver intentionally does not expose an HWND, so tests can inject the
// session hook and exercise persistence without a desktop window.
func setFyneWindowOpacity(w fyne.Window, opacity float64) error {
	if w == nil {
		return nil
	}
	native, ok := w.(driver.NativeWindow)
	if !ok {
		return errors.New("当前窗口后端不支持透明度")
	}
	var result error
	native.RunNative(func(context any) {
		ctx, ok := context.(driver.WindowsWindowContext)
		if !ok || ctx.HWND == 0 {
			result = errors.New("无法获取计时浮窗句柄")
			return
		}
		result = win32.SetWindowOpacity(ctx.HWND, opacity)
	})
	return result
}

func (s *session) nativeOverlayOpacity(opacity float64) error {
	target := s.win
	s.mu.Lock()
	mini := s.miniMode
	s.mu.Unlock()
	if mini && s.miniWin != nil {
		target = s.miniWin
	}
	if target == nil {
		return nil
	}
	if s.overlayOpacity != nil {
		return s.overlayOpacity(target, opacity)
	}
	return setFyneWindowOpacity(target, opacity)
}

// previewAppearance applies changes immediately, but the settings page saves
// them only when the slider interaction ends or the user presses Save.
func (s *session) previewAppearance(opacity, scale float64) error {
	if err := validOverlayAppearance(opacity, scale); err != nil {
		return err
	}
	s.mu.Lock()
	oldOpacity, mini := s.cfg.UI.WindowOpacity, s.miniMode
	s.mu.Unlock()
	// The mini overlay has its own opacity; WindowOpacity returns on exit.
	if opacity != oldOpacity && !mini {
		if err := s.nativeOverlayOpacity(opacity); err != nil {
			s.mu.Lock()
			s.appearanceError = "窗口透明度未应用：" + err.Error()
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Lock()
	s.cfg.UI.WindowOpacity = opacity
	s.cfg.UI.FontScale = scale
	s.appearanceError = ""
	s.mu.Unlock()
	s.applyOverlayFontScale(scale)
	return nil
}

func (s *session) applyOverlayFontScale(scale float64) {
	if s.cd == nil {
		return
	}
	dual := s.alternateBox != nil && s.alternateBox.Visible()
	if dual {
		s.cd.TextSize = overlayTextSize(32, scale)
	} else {
		s.cd.TextSize = overlayTextSize(44, scale)
	}
	if s.primaryLabel != nil {
		s.primaryLabel.TextSize = overlayTextSize(10, scale)
		s.primaryLabel.Refresh()
	}
	if s.altCD != nil {
		s.altCD.TextSize = overlayTextSize(32, scale)
		s.altCD.Refresh()
	}
	if s.alternateLabel != nil {
		s.alternateLabel.TextSize = overlayTextSize(10, scale)
		s.alternateLabel.Refresh()
	}
	if s.alternateSeparator != nil {
		s.alternateSeparator.TextSize = overlayTextSize(24, scale)
		s.alternateSeparator.Refresh()
	}
	if s.eventTag != nil {
		s.eventTag.TextSize = overlayTextSize(13, scale)
		s.eventTag.Refresh()
	}
	if s.tag != nil {
		s.tag.TextSize = overlayTextSize(13, scale)
		s.tag.Refresh()
	}
	if s.info != nil {
		s.info.TextSize = overlayTextSize(11, scale)
		s.info.Refresh()
	}
	// The two-side clocks and their headers follow the same live preview.
	for _, t := range []*canvas.Text{s.leftBothCD, s.rightBothCD} {
		if t != nil {
			t.TextSize = overlayTextSize(36, scale)
			t.Refresh()
		}
	}
	for _, t := range []*canvas.Text{s.leftBothLabel, s.rightBothLabel} {
		if t != nil {
			t.TextSize = overlayTextSize(12, scale)
			t.Refresh()
		}
	}
	s.cd.Refresh()
	if s.overlay != nil {
		s.overlay.Refresh()
	}
	if s.miniStatus != nil {
		s.applyMiniContentScale()
	}
	s.fitOverlayWindow()
}

func (s *session) fitOverlayWindow() {
	if s.win == nil || s.overlay == nil {
		return
	}
	if s.miniWin != nil && s.isMini() {
		s.fitDetachedMiniWindow()
		return
	}
	s.mu.Lock()
	width, height := s.cfg.UI.MiniWidth, s.cfg.UI.MiniHeight
	s.mu.Unlock()
	minimum := fyne.NewSize(float32(max(220, width)), float32(max(118, height+24)))
	s.win.Resize(minimum.Max(s.overlay.MinSize()))
	if s.floatingApplied && s.savedStyle != 0 && s.floatingHook == nil {
		// The backend still sizes with the decorated style it remembers.
		_ = s.runNativeHWND(func(hwnd uintptr) error { return win32.RefitFrameless(hwnd, s.savedStyle) })
	}
}

// applyFullTopmost sets the full overlay's z-order through its own HWND. A
// title lookup cannot find a window that has not been shown yet (mini start)
// and could hit another timer instance.
func (s *session) applyFullTopmost() {
	if s.win == nil {
		return
	}
	s.mu.Lock()
	top := s.topmost
	s.mu.Unlock()
	_ = s.runNativeHWND(func(hwnd uintptr) error { return win32.SetWindowTopmost(hwnd, top) })
}

// rememberWindowPosition/rememberMiniPosition persist where the user left each
// window. A window that was never shown has no HWND and keeps its old value.
func (s *session) rememberWindowPosition() { s.rememberPosition(s.win, false) }
func (s *session) rememberMiniPosition()   { s.rememberPosition(s.miniWin, true) }

func (s *session) rememberPosition(w fyne.Window, mini bool) {
	if w == nil {
		return
	}
	var x, y int32
	if err := s.runNativeWindowHWND(w, func(hwnd uintptr) (err error) {
		x, y, err = win32.WindowPos(hwnd)
		return err
	}); err != nil {
		return
	}
	p := &config.ScreenPoint{X: int(x), Y: int(y)}
	s.mu.Lock()
	if mini {
		s.cfg.UI.MiniPosition = p
	} else {
		s.cfg.UI.WindowPosition = p
	}
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	err := snapshot.Save()
	if err != nil {
		s.debugf("save window position: %v", err)
	}
}

func (s *session) restoreWindowPosition() { s.restorePosition(s.win, false) }
func (s *session) restoreMiniPosition()   { s.restorePosition(s.miniWin, true) }

func (s *session) restorePosition(w fyne.Window, mini bool) {
	if w == nil {
		return
	}
	s.mu.Lock()
	p := s.cfg.UI.WindowPosition
	if mini {
		p = s.cfg.UI.MiniPosition
	}
	s.mu.Unlock()
	if p == nil {
		return
	}
	_ = s.runNativeWindowHWND(w, func(hwnd uintptr) error {
		return win32.MoveWindowOnScreen(hwnd, int32(p.X), int32(p.Y))
	})
}

func (s *session) saveAppearance() error {
	s.mu.Lock()
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	err := snapshot.Save()
	return err
}

func (s *session) appearanceValues() (opacity, scale float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.UI.WindowOpacity, s.cfg.UI.FontScale
}

func (s *session) setAutoCheckUpdates(on bool) error {
	s.mu.Lock()
	s.cfg.UI.AutoCheckUpdates = on
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	err := snapshot.Save()
	if on {
		s.startAutomaticUpdateChecks()
	} else {
		s.stopAutomaticUpdateChecks()
	}
	return err
}

func (s *session) appearanceStatus() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appearanceError
}
