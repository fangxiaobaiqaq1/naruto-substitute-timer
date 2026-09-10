package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// 查克拉青 HUD。背景保持不透明，确保读秒不受底下画面干扰。
var (
	clockIdle = color.NRGBA{R: 184, G: 244, B: 255, A: 255}
	clockLive = color.NRGBA{R: 126, G: 235, B: 255, A: 255}
	clockWarn = color.NRGBA{R: 77, G: 163, B: 255, A: 255}
	tagMine   = color.NRGBA{R: 184, G: 244, B: 255, A: 255}
	tagIdle   = color.NRGBA{R: 126, G: 210, B: 230, A: 255}

	glassBG     = color.NRGBA{R: 6, G: 24, B: 32, A: 255}
	panelBG     = color.NRGBA{R: 10, G: 22, B: 34, A: 255}
	panelBtn    = color.NRGBA{R: 18, G: 48, B: 68, A: 255}
	panelHover  = color.NRGBA{R: 28, G: 72, B: 98, A: 255}
	panelInput  = color.NRGBA{R: 14, G: 36, B: 52, A: 255}
	panelBorder = color.NRGBA{R: 56, G: 140, B: 168, A: 255}
)

type chromaTheme struct{ base fyne.Theme }

func newChromaTheme() fyne.Theme {
	return chromaTheme{base: theme.DefaultTheme()}
}

func (t chromaTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground, theme.ColorNameOverlayBackground, theme.ColorNameMenuBackground,
		theme.ColorNameHeaderBackground, theme.ColorNameShadow, theme.ColorNameSeparator,
		theme.ColorNameScrollBarBackground:
		return glassBG
	case theme.ColorNameButton, theme.ColorNameDisabledButton:
		return panelBtn
	case theme.ColorNameHover, theme.ColorNamePressed, theme.ColorNameFocus, theme.ColorNameSelection:
		return panelHover
	case theme.ColorNameForeground, theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return clockLive
	case theme.ColorNameForegroundOnPrimary:
		return glassBG
	case theme.ColorNameDisabled, theme.ColorNamePlaceHolder:
		return tagIdle
	case theme.ColorNameInputBackground:
		return panelInput
	case theme.ColorNameInputBorder:
		return panelBorder
	}
	return t.base.Color(name, variant)
}

func (t chromaTheme) Font(style fyne.TextStyle) fyne.Resource { return t.base.Font(style) }
func (t chromaTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return t.base.Icon(name)
}
func (t chromaTheme) Size(name fyne.ThemeSizeName) float32 { return t.base.Size(name) }
