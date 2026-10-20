package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// 查克拉青 HUD，与安卓 TimerColors 同一套颜色：青色 = 对面替身冷却中，
// 琥珀色 = 最后 3 秒，灰青 = 无计时。完整浮窗背景保持不透明；
// 仅计时器模式会在原生窗口层抠除 glassBG（因此内容里绝不能使用 glassBG）。
var (
	clockIdle = color.NRGBA{R: 159, G: 185, B: 196, A: 255}
	clockLive = color.NRGBA{R: 92, G: 225, B: 255, A: 255}
	clockWarn = color.NRGBA{R: 255, G: 181, B: 71, A: 255}
	tagMine   = color.NRGBA{R: 227, G: 246, B: 251, A: 255}
	tagIdle   = color.NRGBA{R: 143, G: 179, B: 192, A: 255}

	glassBG     = color.NRGBA{R: 6, G: 24, B: 32, A: 255}
	panelBG     = color.NRGBA{R: 11, G: 26, B: 36, A: 255}
	panelBtn    = color.NRGBA{R: 21, G: 48, B: 63, A: 255}
	panelHover  = color.NRGBA{R: 30, G: 68, B: 87, A: 255}
	panelInput  = color.NRGBA{R: 14, G: 34, B: 46, A: 255}
	panelBorder = color.NRGBA{R: 36, G: 72, B: 90, A: 255}
	textMuted   = color.NRGBA{R: 96, G: 122, B: 133, A: 255}
	exitBG      = color.NRGBA{R: 58, G: 30, B: 36, A: 255}
	exitText    = color.NRGBA{R: 255, G: 154, B: 143, A: 255}

	transparentColor = color.NRGBA{}
)

type chromaTheme struct{ base fyne.Theme }

func newChromaTheme() fyne.Theme {
	return chromaTheme{base: theme.DefaultTheme()}
}

func (t chromaTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground, theme.ColorNameOverlayBackground, theme.ColorNameMenuBackground,
		theme.ColorNameHeaderBackground, theme.ColorNameScrollBarBackground:
		return glassBG
	case theme.ColorNameShadow:
		return color.NRGBA{A: 90}
	case theme.ColorNameSeparator:
		return panelBorder
	case theme.ColorNameButton:
		return panelBtn
	case theme.ColorNameDisabledButton:
		// Disabled buttons must not look clickable.
		return panelInput
	case theme.ColorNameHover, theme.ColorNamePressed, theme.ColorNameFocus, theme.ColorNameSelection:
		return panelHover
	case theme.ColorNameForeground:
		return tagMine
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return clockLive
	case theme.ColorNameForegroundOnPrimary:
		return panelBG
	case theme.ColorNameDisabled:
		return textMuted
	case theme.ColorNamePlaceHolder:
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
func (t chromaTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 6
	}
	return t.base.Size(name)
}
