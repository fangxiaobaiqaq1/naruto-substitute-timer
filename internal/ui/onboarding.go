package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// openOnboarding reuses the normal capture settings controls so discovery,
// validation, persistence and probing always use one implementation.
func (s *session) openOnboarding() {
	if !s.firstRun {
		s.openSettings()
		return
	}
	if s.onboarding != nil {
		s.onboarding.Show()
		s.onboarding.RequestFocus()
		return
	}
	w := fyne.CurrentApp().NewWindow("首次设置模拟器")
	s.onboarding = w
	// stop invokes this callback as well as the capture cancellation callback;
	// closing the guide ensures no onboarding UI remains after shutdown.
	s.onboardingCancel = func() {
		if s.onboarding == w {
			w.Close()
		}
	}
	w.SetOnClosed(func() {
		if s.captureCancel != nil {
			s.captureCancel()
		}
		if s.captureView == w {
			s.captureView = nil
		}
		s.onboarding = nil
		s.onboardingCancel = nil
	})
	step1 := widget.NewLabel(onboardingStep1Text)
	step1.Wrapping = fyne.TextWrapWord
	step2 := widget.NewLabel(onboardingStep2Text)
	step2.Wrapping = fyne.TextWrapWord
	w.SetContent(container.NewBorder(container.NewPadded(container.NewVBox(step1, step2)), nil, nil, nil, s.captureControls(w)))
	w.Resize(fyne.NewSize(650, 680))
	w.Show()
	w.RequestFocus()
}

func (s *session) finishOnboarding() {
	if !s.firstRun {
		return
	}
	s.firstRun = false
	if s.onboarding != nil {
		s.onboarding.Close()
	}
	s.startNormalCapture()
}

const onboardingStep1Text = "1 选择模拟器\n自动发现不可靠或模拟器名称改过时，请手动选择安装目录；实例 0/1 分别是第 1/2 个多开。MuMu SDK 和雷电 ADB 必须先自检成功；自动和窗口截图需确认当前草稿。更改后端或目标后必须重新验证。采集/连接错误会显示在下方。"

const onboardingStep2Text = "2 确认分辨率\n仅适配主流分辨率。本步骤仅作提示，不保存、不阻断；运行中若显示“未校准”，表示当前分辨率不支持。"

// probeSuccessText reports only facts established by the probe. Screenshot
// dimensions alone cannot establish a supported recognition layout.
func probeSuccessText(provider string, width, height int) string {
	return fmt.Sprintf("%s 自检成功：已读取实际 %d × %d 图像，截图连接正常。不能根据截图尺寸判断识别布局或分辨率是否支持。该测试未修改已保存配置。", provider, width, height)
}
