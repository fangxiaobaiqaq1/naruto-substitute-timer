package ui

import (
	"context"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"image/color"
	"narutotimer/internal/buildinfo"
	"narutotimer/internal/updates"
	"net/url"
	"strings"
	"time"
)

func openProjectURL(raw string, w fyne.Window) {
	u, err := url.Parse(raw)
	if err == nil && (u.Scheme != "https" || u.Host != "github.com") {
		err = fmt.Errorf("无效项目链接")
	}
	if err == nil {
		err = fyne.CurrentApp().OpenURL(u)
	}
	if err != nil {
		dialog.ShowError(err, w)
	}
}
func sectionCard(title string, body fyne.CanvasObject) fyne.CanvasObject {
	heading := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	bg := canvas.NewRectangle(color.NRGBA{R: 14, G: 35, B: 47, A: 255})
	bg.CornerRadius = 12
	return container.NewStack(bg, container.NewPadded(container.NewVBox(heading, body)))
}
func changelogText(body string) *widget.RichText {
	// Render release notes as text, never load remote images or run embedded HTML.
	var segments []widget.RichTextSegment
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		style := widget.RichTextStyle{Inline: false}
		if strings.HasPrefix(line, "#") {
			line = strings.TrimLeft(line, "# ")
			style.TextStyle.Bold = true
		}
		if strings.HasPrefix(line, "- ") {
			line = "• " + strings.TrimPrefix(line, "- ")
		}
		line = strings.ReplaceAll(line, "**", "")
		line = strings.ReplaceAll(line, "`", "")
		segments = append(segments, &widget.TextSegment{Text: line, Style: style})
	}
	if len(segments) == 0 {
		segments = append(segments, &widget.TextSegment{Text: "此版本尚未填写更新说明。"})
	}
	r := widget.NewRichText(segments...)
	r.Wrapping = fyne.TextWrapWord
	return r
}
func (s *session) aboutControls(w fyne.Window) fyne.CanvasObject {
	ctx, cancel := context.WithCancel(context.Background())
	s.aboutCancel = cancel
	client := updates.NewClient()
	title := canvas.NewText("替身计时器", clockIdle)
	title.TextSize = 28
	title.TextStyle.Bold = true
	subtitle := widget.NewLabel("纯视觉识别 · 本地 OCR · Windows x64")
	version := widget.NewLabelWithStyle(buildinfo.Version, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	status := widget.NewLabel("点击检查更新，从 GitHub 获取最新正式版。")
	status.Wrapping = fyne.TextWrapWord
	progress := widget.NewProgressBar()
	progress.Hide()
	detail := widget.NewLabel("")
	detail.Wrapping = fyne.TextWrapWord
	date := widget.NewLabel("版本记录来自 GitHub Releases，按发布时间排列。")
	notes := container.NewStack(changelogText("选择版本查看更新内容。"))
	var feed updates.Feed
	var downloaded string
	var downloadCancel context.CancelFunc
	cancelButton := widget.NewButton("取消下载", func() {
		if downloadCancel != nil {
			downloadCancel()
		}
	})
	cancelButton.Hide()
	var selected updates.Release
	picker := widget.NewSelect(nil, nil)
	picker.PlaceHolder = "检查更新后选择版本"
	var refreshHistory func()
	refreshHistory = func() {
		labels := make([]string, len(feed.Releases))
		for i, r := range feed.Releases {
			labels[i] = r.Tag + "  ·  " + r.Published.Local().Format("2006-01-02")
		}
		picker.Options = labels
		picker.Refresh()
		if len(labels) > 0 {
			picker.SetSelected(labels[0])
		}
	}
	picker.OnChanged = func(label string) {
		for i, item := range picker.Options {
			if item == label && i < len(feed.Releases) {
				r := feed.Releases[i]
				date.SetText("发布于 " + r.Published.Local().Format("2006-01-02 15:04") + "  ·  " + r.Name)
				notes.Objects = []fyne.CanvasObject{changelogText(r.Body)}
				notes.Refresh()
				break
			}
		}
	}
	action := widget.NewButtonWithIcon("下载更新", theme.DownloadIcon(), nil)
	action.Importance = widget.HighImportance
	action.Disable()
	check := widget.NewButtonWithIcon("检查更新", theme.ViewRefreshIcon(), nil)
	applyFeed := func(f updates.Feed, cached bool) {
		feed = f
		refreshHistory()
		cmp, err := updates.Compare(f.Latest.Tag, buildinfo.Version)
		prefix := ""
		if cached {
			prefix = "缓存信息 · "
			action.Disable()
		}
		if err != nil {
			status.SetText("当前为开发构建，可查看发布记录。")
			return
		}
		if cmp > 0 {
			status.SetText(prefix + "发现新版本 " + f.Latest.Tag)
			if _, err := f.Latest.Executable(); err == nil && !cached {
				action.Enable()
			} else if err != nil {
				detail.SetText(err.Error())
			}
		}
		if cmp == 0 {
			status.SetText(prefix + "你正在使用最新正式版 " + buildinfo.Version)
			action.Disable()
		}
		if cmp < 0 {
			status.SetText(prefix + "当前版本比 GitHub 已发布版本更新。")
			action.Disable()
		}
		if !f.Checked.IsZero() {
			detail.SetText("上次检查：" + f.Checked.Local().Format("2006-01-02 15:04"))
		}
	}
	if f, err := updates.CachedFeed(); err == nil {
		applyFeed(f, true)
	}
	check.OnTapped = func() {
		check.Disable()
		action.Disable()
		status.SetText("正在连接 GitHub…")
		detail.SetText("")
		go func() {
			request, c := context.WithTimeout(ctx, 25*time.Second)
			defer c()
			f, err := client.Check(request)
			if err == nil {
				_ = updates.SaveFeed(f)
			}
			fyne.Do(func() {
				if ctx.Err() != nil {
					return
				}
				check.Enable()
				if err != nil {
					status.SetText("检查失败，已有更新记录仍可查看。")
					detail.SetText(err.Error())
					return
				}
				downloaded = ""
				action.SetText("下载更新")
				progress.Hide()
				applyFeed(f, false)
			})
		}()
	}
	action.OnTapped = func() {
		if downloaded != "" {
			action.Disable()
			check.Disable()
			status.SetText("正在准备重启…")
			go func() {
				err := updates.PrepareRestart(downloaded, selected)
				fyne.Do(func() {
					if ctx.Err() != nil {
						return
					}
					if err != nil {
						status.SetText("更新未安装，当前版本保持不变。")
						detail.SetText(err.Error())
						action.Enable()
						check.Enable()
						return
					}
					fyne.CurrentApp().Quit()
				})
			}()
			return
		}
		selected = feed.Latest
		downloadCtx, cancelDownload := context.WithCancel(ctx)
		downloadCancel = cancelDownload
		cancelButton.Show()
		action.Disable()
		check.Disable()
		progress.SetValue(0)
		progress.Show()
		status.SetText("正在下载 " + selected.Tag + "…")
		go func() {
			defer cancelDownload()
			file, err := client.Download(downloadCtx, selected, func(done, total int64) {
				fyne.Do(func() {
					if ctx.Err() == nil {
						progress.SetValue(float64(done) / float64(total))
						detail.SetText(fmt.Sprintf("%.1f / %.1f MB", float64(done)/(1<<20), float64(total)/(1<<20)))
					}
				})
			})
			fyne.Do(func() {
				if ctx.Err() != nil {
					return
				}
				check.Enable()
				action.Enable()
				if err != nil {
					cancelButton.Hide()
					status.SetText("下载未完成，当前版本保持不变。")
					detail.SetText(err.Error())
					return
				}
				cancelButton.Hide()
				downloaded = file
				action.SetText("重启并更新")
				status.SetText("下载完成，文件校验通过。")
				detail.SetText("点击重启安装新版本。设置和日志会保留；对局中建议稍后更新。")
			})
		}()
	}
	links := container.NewHBox(widget.NewButton("GitHub 仓库", func() { openProjectURL(updates.RepositoryURL, w) }), widget.NewButton("发布与下载", func() { openProjectURL(updates.RepositoryURL+"/releases", w) }))
	updateCard := sectionCard("软件更新", container.NewVBox(status, detail, progress, container.NewHBox(check, action, cancelButton)))
	history := sectionCard("更新日志", container.NewVBox(picker, date, notes))
	return container.NewVScroll(container.NewVBox(container.NewPadded(container.NewVBox(title, subtitle, version)), updateCard, history, sectionCard("关于项目", container.NewVBox(widget.NewLabel("原创代码采用 MIT 许可；第三方组件保留各自许可。"), links))))
}
