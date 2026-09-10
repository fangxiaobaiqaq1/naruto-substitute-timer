package factory

import (
	"narutotimer/internal/config"
	"os"
	"testing"
)

func TestPlayerTrainingMenuScreenshotRecognized(t *testing.T) {
	const relative = "regressions/camp-player-expanded-toolbar-20260910.png"
	if _, e := os.Stat("../../../inbox/" + relative); os.IsNotExist(e) {
		t.Skip("local player screenshot is not distributed")
	}
	img := stabilityImage(t, relative)
	t.Chdir(t.TempDir())
	e, err := New(FromApp(config.Default()))
	if err != nil {
		t.Fatal(err)
	}
	r := e.Analyze(img)
	if !r.Fighting || r.Uncertain || r.Scene != "fight" || r.LayoutProfile != "camp" || len(r.Beads) != 8 {
		t.Fatalf("player training menu rejected: %+v", r)
	}
	for _, b := range r.Beads {
		if b.Unknown || !b.Lit {
			t.Fatalf("full player HUD bean rejected: %+v", b)
		}
	}
}
