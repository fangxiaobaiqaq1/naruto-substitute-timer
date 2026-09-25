package ui

import (
	"reflect"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"narutotimer/internal/capture"
	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaptureSelectionAppliesAndPersistsWithoutRestart(t *testing.T) {
	a := fynetest.NewApp()
	defer a.Quit()
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), done: make(chan struct{})}
	var selected config.MuMuCaptureConfig
	calls := 0
	s.captureControl = func(c config.MuMuCaptureConfig) uint64 { selected = c; calls++; return uint64(calls) }
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.openSettings()
	defer s.settings.Close()
	s.settingsTabs.SelectIndex(1)
	apply := overlayButton(s.settings.Content(), "保存并应用")
	if apply == nil {
		t.Fatal("missing apply")
	}
	apply.OnTapped()
	cfg, e := config.Load(s.cfgPath)
	if e != nil || cfg.Capture.MuMu.Selection != "auto" || calls != 1 || selected.Selection != "auto" || s.sourceRevision != 1 {
		t.Fatal(cfg.Capture.MuMu, calls, e)
	}
}
func TestStaleSelectedInstanceFrameCannotReachUIOrEventCounter(t *testing.T) {
	a := fynetest.NewApp()
	defer a.Quit()
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), done: make(chan struct{}), sourceRevision: 2, side: "left"}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	old := tracingFrame(time.Now())
	old.SourceRevision = 1
	s.provider = func() frame.Frame { return old }
	s.captureOnce()
	if len(s.beads) != 0 || s.left.EventCount() != 0 || s.right.EventCount() != 0 {
		t.Fatal("old instance observation applied")
	}
	old.SourceRevision = 2
	s.captureOnce()
	if len(s.beads) == 0 {
		t.Fatal("current instance frame discarded")
	}
	_ = os.Remove(s.cfgPath)
}

func TestCaptureSettingsLayoutPinsFooterOutsideScroll(t *testing.T) {
	scrollContent := container.NewVScroll(widget.NewLabel("advanced settings"))
	footer := container.NewVBox(widget.NewLabel("首次设置：保存已锁定"), widget.NewButton("保存并应用", nil))
	layout := captureSettingsLayout(scrollContent, footer)

	border, ok := layout.(*fyne.Container)
	if !ok || border.Layout == nil {
		t.Fatalf("capture settings layout = %#v, want border container", layout)
	}
	if len(border.Objects) != 2 || border.Objects[0] != scrollContent || border.Objects[1] != footer {
		t.Fatalf("capture settings layout children = %#v, want scroll content and fixed footer", border.Objects)
	}
	if scrollContent.Content == footer {
		t.Fatal("fixed footer must not be nested in scroll content")
	}
}

func TestFirstRunNoProbeProvidersAllowOnlyConfirmedExactDraft(t *testing.T) {
	for _, method := range []string{"auto", capture.MethodPrintWindow} {
		t.Run(method, func(t *testing.T) {
			draft := captureConfigWithProvider(config.Default().Capture, method)
			if onboardingDraftReady(true, false, onboardingValidationNone, config.CaptureConfig{}, draft) {
				t.Fatal("unconfirmed first-run draft must remain gated")
			}
			// This models the explicit no-separate-probe confirmation. It must
			// unlock the exact draft without being represented as an image probe.
			confirmed, ok := confirmNoSeparateProbeDraft(method, draft)
			if !ok || !onboardingDraftReady(true, true, onboardingValidationNoSeparateProbe, confirmed, draft) {
				t.Fatal("confirmed no-probe provider should be saveable on first run")
			}
		})
	}
}

func TestFirstRunSDKProvidersRequireMatchingImageProbe(t *testing.T) {
	for _, method := range []string{capture.MethodMuMuSDK, capture.MethodLeidianADB} {
		t.Run(method, func(t *testing.T) {
			draft := captureConfigWithProvider(config.Default().Capture, method)
			if _, ok := confirmNoSeparateProbeDraft(method, draft); ok {
				t.Fatal("no-probe confirmation must not unlock SDK-backed provider")
			}
			if onboardingDraftReady(true, false, onboardingValidationImageProbe, draft, draft) {
				t.Fatal("unsuccessful image probe must not unlock SDK-backed provider")
			}
			if !onboardingDraftReady(true, true, onboardingValidationImageProbe, draft, draft) {
				t.Fatal("successful matching image probe should unlock SDK-backed provider")
			}
		})
	}
}

func TestFirstRunValidationIsInvalidatedByDraftOrProviderChange(t *testing.T) {
	auto := captureConfigWithProvider(config.Default().Capture, "auto")
	if !onboardingDraftReady(true, true, onboardingValidationNoSeparateProbe, auto, auto) {
		t.Fatal("setup: confirmed auto draft should be ready")
	}
	printWindow := captureConfigWithProvider(auto, capture.MethodPrintWindow)
	if onboardingDraftReady(true, true, onboardingValidationNoSeparateProbe, auto, printWindow) {
		t.Fatal("provider change must invalidate previous validation")
	}
	changed := auto
	changed.PreferredMethods = append([]string(nil), auto.PreferredMethods...)
	changed.PreferredMethods = append(changed.PreferredMethods, capture.MethodPrintWindow)
	if onboardingDraftReady(true, true, onboardingValidationNoSeparateProbe, auto, changed) {
		t.Fatal("exact-draft change must invalidate previous validation")
	}
}

func TestCaptureStateTextSeparatesSavedAndAppliedTargets(t *testing.T) {
	saved := config.MuMuCaptureConfig{Selection: "manual", InstallDir: `D:\MuMu`, Instance: 0}
	state := frame.CaptureState{
		RequestedRevision: 4,
		AppliedRevision:   3,
		Requested:         config.MuMuCaptureConfig{Selection: "manual", InstallDir: `E:\MuMu`, Instance: 2},
		Applied:           saved,
		LastError:         "连接 MuMu 实例失败",
	}
	text := captureStateText(`C:\cfg\config.json`, saved, state)
	for _, want := range []string{"已保存：D:\\MuMu · 实例 0", "已请求应用：E:\\MuMu · 实例 2", "采集器已应用：D:\\MuMu · 实例 0", "正在切换", "最近连接/采集异常"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
}

func TestCaptureProviderOptionsAndDraftPreservation(t *testing.T) {
	for _, method := range []string{"auto", capture.MethodMuMuSDK, capture.MethodLeidianADB, capture.MethodPrintWindow} {
		if captureProviderLabel(method) == "" || captureProviderFromLabel(captureProviderLabel(method)) != method {
			t.Fatalf("missing or non-round-trippable active option %q", method)
		}
	}

	original := config.Default().Capture
	original.Provider = "auto"
	original.PreferredMethods = []string{capture.MethodLeidianADB, capture.MethodPrintWindow, capture.MethodMuMuSDK}
	if got := captureConfigWithProvider(original, "auto"); !reflect.DeepEqual(got, original) {
		t.Fatalf("auto config changed without a provider change: got %+v want %+v", got, original)
	}
	if got := captureConfigWithProvider(original, capture.MethodLeidianADB); got.Provider != capture.MethodLeidianADB || !reflect.DeepEqual(got.PreferredMethods, original.PreferredMethods) {
		t.Fatalf("provider switch must be draft-only and retain fallbacks: %+v", got)
	}
}

func TestCaptureSelectingLeidianIsDraftOnlyUntilApply(t *testing.T) {
	original := config.Default().Capture
	draft := captureConfigWithProvider(original, capture.MethodLeidianADB)
	if original.Provider != capture.MethodMuMuSDK || draft.Provider != capture.MethodLeidianADB {
		t.Fatalf("provider selection must change only the draft: saved=%+v draft=%+v", original, draft)
	}
	if !reflect.DeepEqual(original.PreferredMethods, draft.PreferredMethods) {
		t.Fatalf("provider selection unexpectedly rewrote fallbacks: saved=%+v draft=%+v", original, draft)
	}
}

func TestCaptureOpenDoesNotCoerceAutoOrPrintWindow(t *testing.T) {
	for _, method := range []string{"auto", capture.MethodPrintWindow} {
		cfg := config.Default().Capture
		cfg.Provider = method
		cfg.PreferredMethods = []string{capture.MethodPrintWindow, capture.MethodMuMuSDK}
		if got := captureConfigWithProvider(cfg, captureProvider(cfg)); !reflect.DeepEqual(got, cfg) {
			t.Fatalf("opening %s rewrote config: got %+v want %+v", method, got, cfg)
		}
	}
}

func TestCaptureDiscoveryRejectsStaleProviderOrGeneration(t *testing.T) {
	a := fynetest.NewApp()
	defer a.Quit()
	oldView := fynetest.NewTempWindow(t, nil)
	defer oldView.Close()
	newView := fynetest.NewTempWindow(t, nil)
	defer newView.Close()
	if captureDiscoveryCanApply(oldView, oldView, 2, 2, capture.MethodLeidianADB, capture.MethodLeidianADB) != true {
		t.Fatal("current discovery should apply")
	}
	for _, stale := range []bool{
		captureDiscoveryCanApply(oldView, oldView, 1, 2, capture.MethodLeidianADB, capture.MethodLeidianADB),
		captureDiscoveryCanApply(oldView, oldView, 2, 2, capture.MethodLeidianADB, capture.MethodMuMuSDK),
		captureDiscoveryCanApply(oldView, newView, 2, 2, capture.MethodLeidianADB, capture.MethodLeidianADB),
	} {
		if stale {
			t.Fatal("stale discovery result may overwrite newer selection")
		}
	}
}

func TestCaptureProbeSelectionUsesSoleAutomaticCandidate(t *testing.T) {
	items := []mumu.ProcessChoice{{Instance: mumu.Instance{Root: `E:\MuMu`, Index: 2, Name: "训练"}}}
	got, err := captureProbeSelection(0, items, "", "0", config.MuMuCaptureConfig{Selection: "auto", Package: "com.tencent.KiHan"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Selection != "manual" || got.InstallDir != `E:\MuMu` || got.Instance != 2 || got.Package != "com.tencent.KiHan" {
		t.Fatalf("unexpected probe target: %+v", got)
	}
}

func TestCaptureConfigForMuMuProbeKeepsSoleAutomaticTargetForSave(t *testing.T) {
	items := []mumu.ProcessChoice{{Instance: mumu.Instance{Root: `E:\MuMu`, Index: 2, Name: "训练"}}}
	target, err := captureProbeSelection(0, items, "", "0", config.MuMuCaptureConfig{Selection: "auto"})
	if err != nil {
		t.Fatal(err)
	}

	base := config.Default().Capture
	base.PreferredMethods = []string{capture.MethodLeidianADB, capture.MethodPrintWindow, capture.MethodMuMuSDK}
	draft := captureConfigForMuMuProbe(base, target)
	if draft.Provider != capture.MethodMuMuSDK || !reflect.DeepEqual(draft.PreferredMethods, base.PreferredMethods) {
		t.Fatalf("probe unexpectedly rewrote configured fallbacks: %+v", draft)
	}
	if draft.MuMu.Selection != "manual" || draft.MuMu.InstallDir != `E:\MuMu` || draft.MuMu.Instance != 2 {
		t.Fatalf("save draft reverted from verified target: %+v", draft.MuMu)
	}
}

func TestCaptureConfigForMuMuProbeChangesWhenTargetChanges(t *testing.T) {
	probed := captureConfigForMuMuProbe(config.Default().Capture, config.MuMuCaptureConfig{Selection: "manual", InstallDir: `E:\MuMu`, Instance: 2})
	changed := captureConfigForMuMuProbe(probed, config.MuMuCaptureConfig{Selection: "manual", InstallDir: `E:\MuMu`, Instance: 3})
	if reflect.DeepEqual(probed, changed) {
		t.Fatalf("changing the MuMu target must invalidate the probed save config: %+v", changed.MuMu)
	}
}

func TestLeidianSelectionFromFieldsUsesDefaultSerial(t *testing.T) {
	got, err := leidianSelectionFromFields(`E:\leidian\LDPlayer14`, "2", "", config.LeidianCaptureConfig{Package: "com.tencent.KiHan"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Selection != "manual" || got.InstallDir != `E:\leidian\LDPlayer14` || got.Index != 2 || got.Serial != "127.0.0.1:5557" || got.Package != "com.tencent.KiHan" {
		t.Fatalf("unexpected 雷电 target: %+v", got)
	}
}

func TestLeidianProviderConfigIsDistinctFromMuMuDraft(t *testing.T) {
	cfg := config.Default().Capture
	cfg.Provider = capture.MethodLeidianADB
	cfg.PreferredMethods = []string{capture.MethodLeidianADB}
	cfg.Leidian.InstallDir = `E:\leidian\LDPlayer14`
	cfg.Leidian.Index = 2
	if captureProvider(cfg) != capture.MethodLeidianADB || captureProvider(config.Default().Capture) != capture.MethodMuMuSDK {
		t.Fatalf("provider selection was not preserved: %+v", cfg)
	}
	if cfg.MuMu.InstallDir != "" || cfg.MuMu.Instance != 0 {
		t.Fatalf("MuMu draft unexpectedly changed: %+v", cfg.MuMu)
	}
}

func TestCaptureTargetTextIncludesLeidianProvider(t *testing.T) {
	text := captureTargetText(config.CaptureConfig{Provider: capture.MethodLeidianADB, Leidian: config.LeidianCaptureConfig{InstallDir: `E:\leidian\LDPlayer14`, Index: 0}})
	if !strings.Contains(text, "雷电") || !strings.Contains(text, "实例 0") {
		t.Fatalf("unexpected target text: %q", text)
	}
}

func TestCaptureProbeSelectionRejectsAmbiguousAutomaticCandidates(t *testing.T) {
	items := []mumu.ProcessChoice{
		{Instance: mumu.Instance{Root: `D:\MuMu`, Index: 0}},
		{Instance: mumu.Instance{Root: `E:\MuMu`, Index: 0}},
	}
	if _, err := captureProbeSelection(0, items, "", "0", config.MuMuCaptureConfig{Selection: "auto"}); err == nil || !strings.Contains(err.Error(), "2 个 MuMu 实例") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}
