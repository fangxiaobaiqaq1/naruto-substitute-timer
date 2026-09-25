package ui

import (
	"strings"
	"testing"
)

func TestOnboardingTextHasSeparateSelectionAndResolutionSteps(t *testing.T) {
	for _, want := range []string{
		"1 选择模拟器",
		"自动发现不可靠",
		"模拟器名称改过",
		"手动选择安装目录",
		"实例 0/1 分别是第 1/2 个多开",
		"MuMu SDK 和雷电 ADB 必须先自检成功",
		"自动和窗口截图需确认当前草稿",
		"更改后端或目标后必须重新验证",
		"采集/连接错误会显示在下方",
	} {
		if !strings.Contains(onboardingStep1Text, want) {
			t.Fatalf("step 1 missing %q in %q", want, onboardingStep1Text)
		}
	}
	for _, want := range []string{
		"2 确认分辨率",
		"仅适配主流分辨率",
		"不保存、不阻断",
		"运行中若显示“未校准”，表示当前分辨率不支持",
	} {
		if !strings.Contains(onboardingStep2Text, want) {
			t.Fatalf("step 2 missing %q in %q", want, onboardingStep2Text)
		}
	}
}

func TestUnsupportedResolutionSceneLabelStatesCurrentResolutionIsUnsupported(t *testing.T) {
	text := sceneName("unsupported-resolution")
	if !strings.Contains(text, "当前分辨率不支持") {
		t.Fatalf("unsupported-resolution missing explicit reason: %q", text)
	}
}

func TestOnboardingProbeSuccessTextReportsFactsWithoutCalibrationClaim(t *testing.T) {
	text := probeSuccessText("MuMu SDK", 1600, 900)
	for _, want := range []string{
		"已读取实际 1600 × 900 图像",
		"截图连接正常",
		"不能根据截图尺寸判断识别布局或分辨率是否支持",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
	if strings.Contains(text, "画面比例未校准") {
		t.Fatalf("probe success must not claim calibration status: %q", text)
	}
}
