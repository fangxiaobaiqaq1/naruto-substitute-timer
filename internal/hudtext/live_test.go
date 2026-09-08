package hudtext_test

import (
	"image"
	"image/draw"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
)

func TestLiveFactoryOCRWithoutNameTemplates(t *testing.T) {
	if os.Getenv("TIMER_TEST_SYSTEM_OCR") != "1" {
		t.Skip("opt-in installed Windows OCR")
	}
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	cfg.UI.SubstituteTable = filepath.Join("../..", cfg.UI.SubstituteTable)
	cfg.UI.AutoTextRecognition = true
	cfg.UI.PlayerNames = []string{"白方小"}
	// Package CWD has no assets/identity: this test cannot accidentally pass by
	// reading the existing name templates instead of using system text evidence.
	f, err := os.Open("../../inbox/regressions/duel-second-round-20260906.png")
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(source.Bounds())
	draw.Draw(img, img.Bounds(), source, source.Bounds().Min, draw.Src)
	live, err := factory.NewLive(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := live.Close(); err != nil {
			t.Error(err)
		}
	})
	start := time.Now()
	maxCall := time.Duration(0)
	for time.Since(start) < 5*time.Second {
		before := time.Now()
		got := live.AnalyzeAt(img, before)
		maxCall = max(maxCall, time.Since(before))
		if !got.Fighting {
			t.Fatalf("OCR changed battle admission: %+v", got)
		}
		if got.PlayerSide == "right" && got.RightNinja == "山中井野" {
			t.Logf("text confirmed after %v; max Analyze %v; status=%s", time.Since(start), maxCall, got.TextStatus)
			live.SetEnabled(false)
			after := live.AnalyzeAt(img, time.Now())
			if after.RightNinja != "" || after.PlayerSide != "" || after.TextStatus != "off" {
				t.Fatalf("disable kept OCR evidence: %+v", after)
			}
			return
		}
		time.Sleep(16 * time.Millisecond)
	}
	t.Fatal("known text never reached the live result")
}

func TestLiveTextLatencySamples(t *testing.T) {
	if os.Getenv("TIMER_TEST_SYSTEM_OCR") != "1" {
		t.Skip("opt-in runtime latency inspection")
	}
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	cfg.UI.SubstituteTable = filepath.Join("../..", cfg.UI.SubstituteTable)
	f, err := os.Open("../../inbox/regressions/duel-second-round-20260906.png")
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(source.Bounds())
	draw.Draw(img, img.Bounds(), source, source.Bounds().Min, draw.Src)
	for _, enabled := range []bool{false, true} {
		cfg.UI.AutoTextRecognition = enabled
		live, err := factory.NewLive(factory.FromApp(cfg))
		if err != nil {
			t.Fatal(err)
		}
		var ms []float64
		for i := 0; i < 40; i++ {
			beg := time.Now()
			live.AnalyzeAt(img, beg)
			ms = append(ms, time.Since(beg).Seconds()*1000)
			time.Sleep(16 * time.Millisecond)
		}
		live.Close()
		t.Logf("OCR=%v Analyze-only milliseconds: %v", enabled, ms)
	}
}
