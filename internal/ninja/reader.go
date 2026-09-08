package ninja

import (
	"bytes"
	"embed"
	"image"
	_ "image/png"
	"math"
	"sync"

	"narutotimer/internal/match"
)

// Name strips come from user-supplied HUD crops. Special strips establish the
// exact variant; ordinary strips establish only a display name (Slots == 0).
// Neither kind establishes the scene or the player's side.
//
//go:embed templates/*.png
var templates embed.FS

type Palette string

const (
	Warm   Palette = "warm" // Blue/orange six-slot variants.
	Purple Palette = "purple"
	Red    Palette = "red"
)

type Readout struct {
	Name    string
	Slots   int
	Palette Palette
	Score   float64
	// Unverified retains only a recently verified slot topology during a short
	// label gap. Name/Palette/Score are empty; it MUST NOT supply bean votes.
	Unverified bool
}
type nameTemplate struct {
	readout Readout
	gray    *image.Gray
}
type scaledName struct {
	readout Readout
	ncc     *match.PreparedNCC
	size    image.Point
	coarse  *match.PreparedNCC
}
type Reader struct {
	mu       sync.Mutex
	source   []nameTemplate
	scale    float64
	prepared []scaledName
}

func NewReader() *Reader {
	r := &Reader{}
	for _, spec := range []struct {
		file, name string
		slots      int
		palette    Palette
	}{
		{"hashirama", Hashirama, 6, Warm}, {"hashirama_alt", Hashirama, 6, Warm}, {"madara", Madara, 6, Warm},
		{"obito", Obito, 4, Purple}, {"naruto", Naruto, 4, Red}, {"naruto_right", Naruto, 4, Red},
	} {
		data, err := templates.ReadFile("templates/" + spec.file + ".png")
		if err != nil {
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			continue
		}
		r.source = append(r.source, nameTemplate{Readout{Name: spec.name, Slots: spec.slots, Palette: spec.palette}, match.ToGray(img)})
	}
	return r
}

// Read uses scale relative to the supplied 960-wide HUD reference (15px
// nominal slot pitch), never the arbitrary width of a tightly cropped image.
// The cache holds only one scale and owns immutable templates.
func (r *Reader) Read(img *image.RGBA, roi image.Rectangle, scale float64) Readout {
	return r.read(img, roi, scale).Readout
}

type evidence struct {
	Readout
	template *match.PreparedNCC
	rect     image.Rectangle
}

func (r *Reader) read(img *image.RGBA, roi image.Rectangle, scale float64) evidence {
	if r == nil || img == nil || scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return evidence{}
	}
	r.mu.Lock()
	if r.prepared == nil || r.scale != scale {
		r.prepared = nil
		r.scale = scale
		for _, t := range r.source {
			w, h := int(math.Round(float64(t.gray.Bounds().Dx())*scale)), int(math.Round(float64(t.gray.Bounds().Dy())*scale))
			if w < 8 || h < 8 {
				continue
			}
			gray := match.ScaleGray(t.gray, w, h)
			coarseScale := min(scale, .5)
			small := match.ScaleGray(t.gray, int(math.Round(float64(t.gray.Bounds().Dx())*coarseScale)), int(math.Round(float64(t.gray.Bounds().Dy())*coarseScale)))
			r.prepared = append(r.prepared, scaledName{readout: t.readout, ncc: match.PrepareNCC(gray, nil), size: gray.Bounds().Size(), coarse: match.PrepareNCC(small, nil)})
		}
	}
	prepared := r.prepared
	r.mu.Unlock()
	roi = roi.Intersect(img.Bounds())
	if roi.Empty() {
		return evidence{}
	}
	view := img.SubImage(roi).(*image.RGBA)
	gray := match.ToGray(view)
	// Unknown names used to scan the whole corner at native resolution. Keep
	// the locator bounded at the 480-wide HUD scale, then validate candidate
	// locations using ALL original-resolution pixels and the original threshold.
	factor := min(scale, .5) / scale
	smallGray := match.ScaleGray(gray, max(1, int(math.Round(float64(gray.Bounds().Dx())*factor))), max(1, int(math.Round(float64(gray.Bounds().Dy())*factor))))
	smallImage := image.NewRGBA(smallGray.Bounds())
	var best evidence
	scores := map[string]float64{}
	for _, t := range prepared {
		coarse, err := (match.NCC{}).Match(match.Query{Image: smallImage, Gray: smallGray, ROI: smallImage.Bounds(), Prepared: t.coarse})
		if err != nil || coarse.Value < .55 {
			continue
		}
		point := image.Pt(roi.Min.X+int(math.Round(float64(coarse.Peak.X)/factor)), roi.Min.Y+int(math.Round(float64(coarse.Peak.Y)/factor)))
		radius := int(math.Ceil(2 / factor))
		candidate := image.Rectangle{Min: point, Max: point.Add(t.size)}.Inset(-radius).Intersect(roi)
		score, err := (match.NCC{}).Match(match.Query{Image: view, Gray: gray, ROI: candidate, Prepared: t.ncc})
		if err != nil {
			continue
		}
		scores[t.readout.Name] = max(scores[t.readout.Name], score.Value)
		if score.Value > best.Score {
			best = evidence{Readout: t.readout, template: t.ncc, rect: image.Rectangle{Min: score.Peak, Max: score.Peak.Add(t.size)}}
			best.Score = score.Value
		}
	}
	runnerUp := 0.0
	for name, score := range scores {
		if name != best.Name {
			runnerUp = max(runnerUp, score)
		}
	}
	if best.Score < 0.80 || best.Score-runnerUp < 0.08 {
		return evidence{}
	}
	return best
}

// NameRegion is deliberately above the calibrated bean row. Blood bars and
// full-screen effects do not get to move the sampling centers.
func NameRegion(first image.Point, scale float64, left bool) image.Rectangle {
	x0, x1 := -6.0, 340.0
	if !left {
		x0, x1 = -340.0, 6.0
	}
	return image.Rect(first.X+int(math.Round(x0*scale)), first.Y-int(math.Round(48*scale)), first.X+int(math.Round(x1*scale)), first.Y-int(math.Round(13*scale)))
}
