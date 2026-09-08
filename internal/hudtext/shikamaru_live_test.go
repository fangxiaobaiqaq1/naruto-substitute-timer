package hudtext_test

import (
	"image"
	"image/draw"
	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveShikamaruOCRFailureIsNotFabricatedIdentity(t *testing.T) {
	if os.Getenv("TIMER_TEST_SYSTEM_OCR") != "1" {
		t.Skip("installed OCR opt-in")
	}
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	cfg.UI.SubstituteTable = filepath.Join("../..", cfg.UI.SubstituteTable)
	cfg.UI.AutoTextRecognition = true
	cfg.UI.PlayerNames = []string{"白方小"}
	f, err := os.Open("../../inbox/regressions/duel-shikamaru-name-20260907.png")
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
	defer live.Close()
	start := time.Now()
	for time.Since(start) < 1500*time.Millisecond {
		got := live.AnalyzeAt(img, time.Now())
		if got.RightNinja != "" || got.RightNinjaCandidate != "" {
			t.Fatalf("uncertain short library name guessed: %+v", got)
		}
		if got.RightSlots != 4 {
			t.Fatalf("OCR affected topology: %+v", got)
		}
		for _, b := range got.Beads {
			if b.Unknown {
				t.Fatalf("OCR affected beans: %+v", got)
			}
		}
		time.Sleep(16 * time.Millisecond)
	}
	t.Log("Known unresolved case: Windows OCR reads 丸 as 九; library match conservatively remains unconfirmed. Beans remain known.")
}
