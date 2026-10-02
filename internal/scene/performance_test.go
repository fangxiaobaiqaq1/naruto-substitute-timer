package scene

import (
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/match"
)

// Optional repository recordings measure the complete scene gate without file I/O.
func BenchmarkCatalogRecordedFrames(b *testing.B) {
	for _, sample := range []struct{ name, path string }{
		{"camp", "inbox/fight/duel_live_20260821.png"},
		{"duel", "inbox/fight/duel_20260821.png"},
		{"lobby", "debug/lab-sdk-probe-20260906/frame-000001.png"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			f, err := os.Open(filepath.Join("../..", sample.path))
			if err != nil {
				b.Skip(err)
			}
			src, _, err := image.Decode(f)
			f.Close()
			if err != nil {
				b.Fatal(err)
			}
			img := image.NewRGBA(src.Bounds())
			draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
			cfg, err := config.Load("../../config.json")
			if err != nil {
				b.Fatal(err)
			}
			cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
			cat, err := Load(cfg)
			if err != nil || cat == nil {
				b.Fatalf("load catalog: %v", err)
			}
			b.Logf("decision: %+v", cat.Decide(img))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cat.Decide(img)
			}
		})
	}
}

func BenchmarkSceneTemplateCosts(b *testing.B) {
	cfg, err := config.Load("../../config.json")
	if err != nil {
		b.Fatal(err)
	}
	cfg.Scene.Manifest = filepath.Join("../..", cfg.Scene.Manifest)
	c, err := Load(cfg)
	if err != nil {
		b.Fatal(err)
	}
	path := os.Getenv("TIMER_BENCH_IMAGE")
	if path == "" {
		path = "../../inbox/regressions/duel-second-round-20260906.png"
	}
	f, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		b.Fatal(err)
	}
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	gray := match.ToGray(img)
	ca, _ := c.contentArea(img)
	for _, t := range c.prepare(ca) {
		b.Run(t.spec.ID, func(b *testing.B) {
			hit, _ := c.matcher.Match(match.Query{Image: img, Gray: gray, ROI: t.roi, Template: t.gray, Mask: t.mask})
			b.Logf("peak=%v score=%.3f", hit.Peak, hit.Value)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.scoreOne(img, gray, t)
			}
		})
	}
}

// A cold decide scores every template over its full ROI: noise never accepts
// a template, so no peak is remembered and every frame rescans.
func BenchmarkCatalogColdDecide(b *testing.B) {
	cat := embeddedCatalog(b)
	img := synthFrame(b, embeddedCatalog(b), 1920, 1080, 1, image.Point{})
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	b.Logf("decision: %+v", cat.DecideAt(img, at))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at = at.Add(33 * time.Millisecond)
		cat.DecideAt(img, at)
	}
}

// A hinted fight decide checks remembered peaks every frame and rescans the
// cold templates on the bounded cadence, as during a confirmed 30 fps fight.
func BenchmarkCatalogHintedFightDecide(b *testing.B) {
	for _, tc := range []struct {
		name string
		ids  []string
	}{
		{"camp", []string{"fight-scene-key", "raw-camp-key"}},
		{"duel", []string{"raw-duel-round", "fight-round", "raw-duel-opening-60"}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			cat := embeddedCatalog(b)
			img := synthFrame(b, embeddedCatalog(b), 1920, 1080, 2, image.Point{}, tc.ids...)
			at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			b.Logf("decision: %+v", cat.DecideAt(img, at))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				at = at.Add(33 * time.Millisecond)
				cat.DecideAt(img, at)
			}
		})
	}
}
