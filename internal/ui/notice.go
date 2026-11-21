package ui

import (
	"fmt"
	"image/color"
	"net/url"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"narutotimer/internal/updates"
)

// The launch notice is shown on every start. It is fixed text compiled into
// the binary: nothing is fetched, so it works offline and cannot be spoofed.
const (
	noticeTitle = "公告"
	noticeBody  = "近期有闲鱼倒卖本开源软件，作者对售后不负责，本软件永久开源免费。"
	noticeHint  = "请只从下面的官方仓库免费下载，付费购买的版本可能被改动过。"
)

var noticeAccent = color.NRGBA{R: 255, G: 112, B: 96, A: 255}

// openOfficialURL opens only the two official repositories' https links.
func openOfficialURL(raw string, w fyne.Window) {
	u, err := url.Parse(raw)
	if err == nil && (u.Scheme != "https" || (u.Host != "github.com" && u.Host != "gitee.com")) {
		err = fmt.Errorf("无效项目链接")
	}
	if err == nil {
		err = fyne.CurrentApp().OpenURL(u)
	}
	if err != nil && w != nil {
		dialog.ShowError(err, w)
	}
}

// noticeContent is the launch notice body; onClose dismisses it.
func noticeContent(w fyne.Window, onClose func()) fyne.CanvasObject {
	icon := widget.NewIcon(theme.WarningIcon())
	title := canvas.NewText(noticeTitle, noticeAccent)
	title.TextSize = 20
	title.TextStyle.Bold = true
	header := container.NewHBox(icon, title)

	body := widget.NewLabel(noticeBody)
	body.Wrapping = fyne.TextWrapWord
	body.TextStyle.Bold = true
	stripe := canvas.NewRectangle(noticeAccent)
	stripe.SetMinSize(fyne.NewSize(4, 0))
	cardBG := canvas.NewRectangle(color.NRGBA{R: 44, G: 22, B: 26, A: 255})
	cardBG.CornerRadius = 10
	cardBG.StrokeColor = color.NRGBA{R: 120, G: 52, B: 50, A: 255}
	cardBG.StrokeWidth = 1
	card := container.NewStack(cardBG, container.NewPadded(container.NewBorder(nil, nil, stripe, nil, body)))

	hint := widget.NewLabel(noticeHint)
	hint.Wrapping = fyne.TextWrapWord
	hint.Importance = widget.LowImportance
	github := widget.NewButtonWithIcon("GitHub 仓库", theme.ComputerIcon(), func() { openOfficialURL(updates.RepositoryURL, w) })
	gitee := widget.NewButtonWithIcon("Gitee 仓库（国内）", theme.HomeIcon(), func() { openOfficialURL(updates.GiteeRepositoryURL, w) })
	links := container.NewGridWithColumns(2, github, gitee)

	ok := widget.NewButtonWithIcon("我知道了", theme.ConfirmIcon(), onClose)
	ok.Importance = widget.HighImportance
	footer := container.NewHBox(layout.NewSpacer(), ok)

	return container.NewPadded(container.NewVBox(header, card, hint, links, widget.NewSeparator(), footer))
}

// showLaunchNotice opens the notice in its own small window on top of the
// overlay. It is informational only and never blocks the timer loop.
func (s *session) showLaunchNotice() {
	w := fyne.CurrentApp().NewWindow("替身 · " + noticeTitle)
	w.SetContent(noticeContent(w, w.Close))
	w.Resize(fyne.NewSize(420, 0))
	w.SetFixedSize(true)
	w.CenterOnScreen()
	w.Show()
	w.RequestFocus()
}

func (s *session) setShowNotice(on bool) error {
	s.mu.Lock()
	s.cfg.UI.ShowNotice = on
	snapshot := s.settingsSnapshotLocked()
	s.mu.Unlock()
	return snapshot.Save()
}

// noticeToggle lets users turn the launch notice off (it defaults on).
func (s *session) noticeToggle(w fyne.Window) fyne.CanvasObject {
	s.mu.Lock()
	on := s.cfg.UI.ShowNotice
	s.mu.Unlock()
	chk := widget.NewCheck("启动时显示公告", nil)
	chk.SetChecked(on)
	chk.OnChanged = func(on bool) { showSettingsError(s.setShowNotice(on), w) }
	return chk
}
