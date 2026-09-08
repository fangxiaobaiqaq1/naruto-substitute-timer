package hudtext

import (
	"context"
	"encoding/json"
	"image"
	"image/draw"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/ocr"
)

func realFrame(t testing.TB, file string) *image.RGBA {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	return img
}

func TestInstalledOCRRecordedNames(t *testing.T) {
	if os.Getenv("TIMER_TEST_SYSTEM_OCR") != "1" {
		t.Skip("opt-in Windows OCR integration")
	}
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.SubstituteTable = filepath.Join("../..", cfg.UI.SubstituteTable)
	reader := ocr.NewSystem()
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	})
	var records []Report
	for _, name := range []string{"duel-player-left-20260906.png", "duel-user-20260906.png", "duel-second-round-20260906.png"} {
		img := realFrame(t, filepath.Join("../../inbox/regressions", name))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		report, err := Diagnose(ctx, reader, img, cfg, "duel")
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, report)
		raw, _ := json.Marshal(report)
		t.Logf("%s: %s", name, raw)
		if name == "duel-second-round-20260906.png" && (report.Titles[1].Ninja != "山中井野" || report.Titles[1].Account != "白方小") {
			t.Fatalf("clear name crop regression: %+v", report)
		}
	}
	if output := os.Getenv("TIMER_OCR_REPORT"); output != "" {
		data, _ := json.MarshalIndent(records, "", "  ")
		if err := os.WriteFile(output, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
