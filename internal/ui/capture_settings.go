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
	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/config"
	"strconv"
	"strings"
	"time"
)

func WithCaptureSelection(selectTarget func(config.MuMuCaptureConfig) uint64) Option {
	return func(s *session) { s.captureControl = selectTarget }
}
func WithInitialAbout(show bool) Option { return func(s *session) { s.initialAbout = show } }
func (s *session) captureControls(w fyne.Window) fyne.CanvasObject {
	base, cancelView := context.WithCancel(context.Background())
	s.captureCancel = cancelView
	s.mu.Lock()
	current, source := s.cfg.Capture.MuMu, s.captureSource
	s.mu.Unlock()
	root := widget.NewEntry()
	root.SetPlaceHolder("一般不需要填写；可在这里手动指定 MuMu 安装目录")
	root.SetText(current.InstallDir)
	number := widget.NewEntry()
	number.SetText(strconv.Itoa(current.Instance))
	status := widget.NewLabel("正在查找运行中的 MuMu 窗口和多开实例…")
	status.Wrapping = fyne.TextWrapWord
	if source == "" {
		source = "等待连接"
	}
	active := widget.NewLabel("当前来源：" + source)
	active.Wrapping = fyne.TextWrapWord
	var items []mumu.ProcessChoice
	selected := 0
	var choices *widget.List
	var generation uint64
	applyTarget := func(next config.MuMuCaptureConfig, label string) {
		s.mu.Lock()
		cfg := s.cfg
		cfg.Capture.MuMu = next
		err := config.Save(s.cfgPath, cfg)
		if err == nil {
			s.cfg = cfg
			s.left.Reset()
			s.right.Reset()
			s.clearAutoIdentity()
			s.ninjaDisplays = [2]ninjaDisplay{}
			if s.captureControl != nil {
				s.sourceRevision = s.captureControl(next)
			}
		}
		s.mu.Unlock()
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		current = next
		status.SetText("已切换：" + label + "。正在连接，设置已记住。")
		active.SetText("已选择：" + label)
		s.refreshOverlay()
	}
	useSelected := func() {
		next := current
		next.DLLPath = ""
		if selected == 0 {
			next.Selection = "auto"
			next.Instance = 0
			next.InstallDir = ""
			applyTarget(next, "自动检测")
			return
		}
		if selected > len(items) {
			return
		}
		item := items[selected-1]
		next.Selection = "manual"
		next.InstallDir = item.Root
		next.Instance = item.Index
		applyTarget(next, fmt.Sprintf("%s · 实例 %d", item.Name, item.Index))
	}
	choices = widget.NewList(func() int { return len(items) + 1 }, func() fyne.CanvasObject {
		title := widget.NewLabelWithStyle("MuMu 安卓设备", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		details := widget.NewLabel("窗口进程信息")
		details.Truncation = fyne.TextTruncateEllipsis
		return container.NewBorder(nil, nil, widget.NewIcon(theme.ComputerIcon()), nil, container.NewVBox(title, details))
	}, func(id widget.ListItemID, obj fyne.CanvasObject) {
		group := obj.(*fyne.Container)
		labels := group.Objects[0].(*fyne.Container)
		title := labels.Objects[0].(*widget.Label)
		detail := labels.Objects[1].(*widget.Label)
		if id == 0 {
			title.SetText("自动检测（推荐）")
			detail.SetText("自动寻找正在运行火影忍者的实例")
			return
		}
		item := items[id-1]
		name := item.Name
		if item.WindowTitle != "" {
			name = item.WindowTitle
		}
		title.SetText(fmt.Sprintf("%s · 实例 %d", name, item.Index))
		state := "未启动"
		if item.Running {
			state = "运行中"
		}
		if item.PID > 0 {
			detail.SetText(fmt.Sprintf("%s  ·  PID %d  ·  %s", item.ProcessName, item.PID, state))
		} else {
			detail.SetText(state + "  ·  " + item.Root)
		}
	})
	choices.OnSelected = func(id widget.ListItemID) { selected = id; useSelected() }
	refresh := widget.NewButtonWithIcon("刷新进程", theme.ViewRefreshIcon(), nil)
	refresh.OnTapped = func() {
		generation++
		now := generation
		refresh.Disable()
		status.SetText("正在枚举 MuMu 窗口与实例…")
		directory := strings.TrimSpace(root.Text)
		go func() {
			ctx, cancel := context.WithTimeout(base, 10*time.Second)
			defer cancel()
			found, err := mumu.ListProcessChoices(ctx, directory)
			fyne.Do(func() {
				if base.Err() != nil || s.stopped() || s.settings != w || now != generation {
					return
				}
				refresh.Enable()
				if err != nil {
					status.SetText("没有找到可选实例。请先启动 MuMu 后点击刷新；也可展开高级设置。\n" + err.Error())
					return
				}
				items = found
				choices.Refresh()
				selected = 0
				if current.Selection == "manual" || (current.Selection == "" && current.Instance != 0) {
					for i, v := range items {
						if v.Index == current.Instance && (current.InstallDir == "" || strings.EqualFold(v.Root, current.InstallDir)) {
							selected = i + 1
							break
						}
					}
				}
				handler := choices.OnSelected
				choices.OnSelected = nil
				choices.Select(selected)
				choices.OnSelected = handler
				status.SetText(fmt.Sprintf("找到 %d 个实例。直接点击列表即可切换采集进程。", len(items)))
			})
		}()
	}
	s.captureRefresh = refresh.OnTapped
	browse := widget.NewButton("浏览安装目录", func() {
		dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if uri != nil {
				root.SetText(uri.Path())
				refresh.OnTapped()
			}
		}, w).Show()
	})
	manual := widget.NewButton("连接手动填写的实例", func() {
		id, err := strconv.Atoi(strings.TrimSpace(number.Text))
		if err != nil || id < 0 {
			dialog.ShowError(fmt.Errorf("实例编号需要填写非负整数"), w)
			return
		}
		next := current
		next.Selection = "manual"
		next.Instance = id
		next.InstallDir = strings.TrimSpace(root.Text)
		next.DLLPath = ""
		applyTarget(next, fmt.Sprintf("实例 %d", id))
	})
	advanced := widget.NewAccordion(widget.NewAccordionItem("高级设置：手动指定安装目录与实例", container.NewVBox(widget.NewLabel("MuMu 安装目录"), root, browse, widget.NewLabel("多开管理器中的实例编号"), number, manual)))
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(0, 240))
	apply := widget.NewButton("使用选中的进程", useSelected)
	apply.Importance = widget.HighImportance
	return container.NewVScroll(container.NewVBox(sectionCard("选择模拟器进程", container.NewVBox(active, status, refresh, container.NewStack(spacer, choices), apply)), advanced))
}
