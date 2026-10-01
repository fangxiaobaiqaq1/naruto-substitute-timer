package factory

import (
	"bytes"
	"crypto/sha256"
	"hash/maphash"
	"image"
	"image/color"
	"image/draw"
	_ "image/png"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/assets"
	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/scene"
)

// syntheticDuelFrame paints a pseudo game frame: textured background, the real
// duel round marker at its manifest ROI (so the gate accepts a duel fight) and
// eight readable blue beads at the calibrated duel slots.
func syntheticDuelFrame(tb testing.TB, cfg config.Config, w, h int, lit int) *image.RGBA {
	tb.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*img.Stride + x*4
			base := uint8(40 + (x*97/w+y*61/h)%90)
			n := uint8(rng.Intn(24))
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = base+n, base/2+n, base+20+n, 255
		}
	}
	data, err := assets.Templates.ReadFile("templates/raw_duel_round.png")
	if err != nil {
		tb.Fatal(err)
	}
	marker, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		tb.Fatal(err)
	}
	// manifest roi for raw-duel-round: x 0.4625 y 0.015 (reference 1920x1080)
	mx, my := int(0.4625*float64(w))+8, int(0.015*float64(h))+6
	draw.Draw(img, image.Rect(mx, my, mx+marker.Bounds().Dx()*w/1920, my+marker.Bounds().Dy()*h/1080), marker, marker.Bounds().Min, draw.Src)
	layout := engine.NewConfiguredLayout(detect.ModeAuto, cfg.Layout)
	area, ok := layout.ContentArea(img)
	if !ok {
		tb.Fatal("content area")
	}
	for _, p := range layout.PositionsIn("duel", area) {
		c := color.RGBA{R: 30, G: 60, B: 110, A: 255} // dark (cooling)
		if p.Idx < lit {
			c = color.RGBA{R: 40, G: 200, B: 255, A: 255} // lit blue
		}
		for dy := -8; dy <= 8; dy++ {
			for dx := -6; dx <= 6; dx++ {
				if detect.InBeadDiamond(0, 0, 12, 16, dx, dy) {
					img.SetRGBA(p.X+dx, p.Y+dy, c)
				}
			}
		}
	}
	return img
}

func benchConfig(tb testing.TB) config.Config {
	cfg := config.Default()
	cfg.Scene.Manifest = filepath.Join("../../..", cfg.Scene.Manifest)
	return cfg
}

func BenchmarkSyntheticGateDecide(b *testing.B) {
	cfg := benchConfig(b)
	img := syntheticDuelFrame(b, cfg, 1920, 1080, 4)
	gate, name, err := scene.NewGate(cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("gate=%s decision=%+v", name, gate.Decide(img))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gate.Decide(img)
	}
}

func BenchmarkSyntheticEngineAnalyze(b *testing.B) {
	cfg := benchConfig(b)
	img := syntheticDuelFrame(b, cfg, 1920, 1080, 4)
	eng, err := New(FromApp(cfg))
	if err != nil {
		b.Fatal(err)
	}
	timed := eng.(engine.TimedEngine)
	at := time.Unix(1700000000, 0)
	r := timed.AnalyzeAt(img, at)
	b.Logf("result fighting=%v scene=%s profile=%s beads=%d uncertain=%v", r.Fighting, r.Scene, r.LayoutProfile, len(r.Beads), r.Uncertain)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at = at.Add(16 * time.Millisecond)
		timed.AnalyzeAt(img, at)
	}
}

func BenchmarkSyntheticLiveEngineAnalyze(b *testing.B) {
	cfg := benchConfig(b)
	img := syntheticDuelFrame(b, cfg, 1920, 1080, 4)
	eng, err := NewLive(FromApp(cfg))
	if err != nil {
		b.Fatal(err)
	}
	defer eng.Close()
	at := time.Unix(1700000000, 0)
	eng.AnalyzeAt(img, at)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at = at.Add(16 * time.Millisecond)
		eng.AnalyzeAt(img, at)
	}
}

func BenchmarkFingerprintSHA256(b *testing.B) {
	pix := make([]byte, 1920*1080*4)
	b.SetBytes(int64(len(pix)))
	for i := 0; i < b.N; i++ {
		sha256.Sum256(pix)
	}
}

func BenchmarkFingerprintMaphash(b *testing.B) {
	pix := make([]byte, 1920*1080*4)
	seed := maphash.MakeSeed()
	b.SetBytes(int64(len(pix)))
	for i := 0; i < b.N; i++ {
		maphash.Bytes(seed, pix)
	}
}

func BenchmarkFlipCopyAlloc(b *testing.B) {
	src := make([]byte, 1920*1080*4)
	b.SetBytes(int64(len(src)))
	for i := 0; i < b.N; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
		stride := 1920 * 4
		for y := 0; y < 1080; y++ {
			copy(img.Pix[y*stride:(y+1)*stride], src[(1080-1-y)*stride:(1080-y)*stride])
		}
		for index := 3; index < len(img.Pix); index += 4 {
			img.Pix[index] = 255
		}
	}
}
