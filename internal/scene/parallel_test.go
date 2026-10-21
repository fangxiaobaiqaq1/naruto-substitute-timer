package scene

import (
	"image"
	"image/color"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/match"
)

// serialNCC scores exactly like match.NCC but is a different matcher type, so
// the catalog must keep scoring it one template after another.
type serialNCC struct{ match.NCC }

func embeddedCatalog(tb testing.TB) *Catalog {
	tb.Helper()
	cat, err := Load(config.Default())
	if err != nil || cat == nil {
		tb.Fatalf("load embedded catalog: %v, %v", cat, err)
	}
	return cat
}

// synthFrame paints deterministic noise and pastes the named prepared
// templates (scaled for this geometry) near the centre of their ROI.
func synthFrame(tb testing.TB, builder *Catalog, w, h int, seed uint64, offset image.Point, ids ...string) *image.RGBA {
	tb.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = uint8(rng.UintN(256))
		img.Pix[i+1] = uint8(rng.UintN(256))
		img.Pix[i+2] = uint8(rng.UintN(256))
		img.Pix[i+3] = 255
	}
	ca, ok := builder.contentArea(img)
	if !ok {
		tb.Fatalf("unsupported synthetic geometry %dx%d", w, h)
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, p := range builder.prepare(ca) {
		if !want[p.spec.ID] || p.gray == nil {
			continue
		}
		delete(want, p.spec.ID)
		size := p.gray.Bounds().Size()
		at := p.roi.Min.Add(p.roi.Size().Sub(size).Div(2)).Add(offset)
		for y := 0; y < size.Y; y++ {
			for x := 0; x < size.X; x++ {
				v := p.gray.GrayAt(p.gray.Bounds().Min.X+x, p.gray.Bounds().Min.Y+y).Y
				img.SetRGBA(at.X+x, at.Y+y, color.RGBA{R: v, G: v, B: v, A: 255})
			}
		}
	}
	if len(want) != 0 {
		tb.Fatalf("unknown synthetic templates: %v", want)
	}
	return img
}

type catalogState struct {
	area       detect.ContentArea
	hints      []templateHint
	lastCold   []Hit
	coldAt     time.Time
	coldFrames int
}

func snapshotState(c *Catalog) catalogState {
	c.prepareMu.Lock()
	area := c.preparedArea
	c.prepareMu.Unlock()
	c.hintMu.Lock()
	defer c.hintMu.Unlock()
	return catalogState{
		area:       area,
		hints:      append([]templateHint(nil), c.hints...),
		lastCold:   append([]Hit(nil), c.lastCold...),
		coldAt:     c.coldAt,
		coldFrames: c.coldFrames,
	}
}

type synthStep struct {
	name   string
	w, h   int
	seed   uint64
	offset image.Point
	dt     time.Duration // Relative to the previous frame; negative seeks back.
	ids    []string
}

func synthSequence() []synthStep {
	camp := []string{"fight-scene-key", "raw-camp-key"}
	duel := []string{"raw-duel-round", "fight-round", "raw-duel-opening-60"}
	lobby := []string{"duel-lobby-ninjutsu", "duel-lobby-ranked", "raw-lobby-labels"}
	steps := []synthStep{
		{name: "noise", w: 1920, h: 1080, seed: 1},
		{name: "lobby", w: 1920, h: 1080, seed: 2, dt: 33 * time.Millisecond, ids: lobby},
	}
	for i := 0; i < 8; i++ {
		steps = append(steps, synthStep{name: "camp", w: 1920, h: 1080, seed: 10 + uint64(i), dt: 33 * time.Millisecond, ids: camp})
	}
	steps = append(steps,
		synthStep{name: "camp-shift-in-window", w: 1920, h: 1080, seed: 20, offset: image.Pt(1, 1), dt: 33 * time.Millisecond, ids: camp},
		synthStep{name: "camp-shift-out-of-window", w: 1920, h: 1080, seed: 21, offset: image.Pt(4, 0), dt: 33 * time.Millisecond, ids: camp},
		synthStep{name: "camp-long-gap", w: 1920, h: 1080, seed: 22, dt: 250 * time.Millisecond, ids: camp},
		synthStep{name: "camp-seek-back", w: 1920, h: 1080, seed: 23, dt: -time.Second, ids: camp},
		synthStep{name: "duel", w: 1920, h: 1080, seed: 30, dt: 33 * time.Millisecond, ids: duel},
		synthStep{name: "duel", w: 1920, h: 1080, seed: 31, dt: 33 * time.Millisecond, ids: duel},
		synthStep{name: "duel+lobby", w: 1920, h: 1080, seed: 32, dt: 33 * time.Millisecond, ids: append(append([]string{}, duel...), lobby...)},
		synthStep{name: "noise", w: 1920, h: 1080, seed: 33, dt: 33 * time.Millisecond},
		synthStep{name: "result", w: 1920, h: 1080, seed: 34, dt: 33 * time.Millisecond, ids: []string{"result-detail", "result-actions"}},
		synthStep{name: "vs", w: 1920, h: 1080, seed: 35, dt: 33 * time.Millisecond, ids: []string{"vs-mark"}},
	)
	for i := 0; i < 5; i++ {
		steps = append(steps, synthStep{name: "camp-720p", w: 1280, h: 720, seed: 40 + uint64(i), dt: 20 * time.Millisecond, ids: camp})
	}
	steps = append(steps,
		synthStep{name: "lobby-720p", w: 1280, h: 720, seed: 50, dt: 33 * time.Millisecond, ids: lobby},
		synthStep{name: "noise-720p", w: 1280, h: 720, seed: 51, dt: 33 * time.Millisecond},
	)
	return steps
}

// The parallel NCC path must reproduce the sequential scorer exactly: the same
// decisions, the same per-template scores and the same hint/cold-scan state.
func TestParallelScoringMatchesSequential(t *testing.T) {
	builder := embeddedCatalog(t)
	parDecide, seqDecide := embeddedCatalog(t), embeddedCatalog(t)
	parScore, seqScore := embeddedCatalog(t), embeddedCatalog(t)
	seqDecide.matcher, seqScore.matcher = serialNCC{}, serialNCC{}
	if !parDecide.parallelScoring() || seqDecide.parallelScoring() {
		t.Fatal("catalogs did not select the intended scoring paths")
	}
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	var fights, skippedCold, hinted int
	for i, step := range synthSequence() {
		at = at.Add(step.dt)
		img := synthFrame(t, builder, step.w, step.h, step.seed, step.offset, step.ids...)

		dp, ds := parDecide.DecideAt(img, at), seqDecide.DecideAt(img, at)
		if dp != ds {
			t.Fatalf("frame %d %s: parallel decision %+v, sequential %+v", i, step.name, dp, ds)
		}
		if sp, ss := snapshotState(parDecide), snapshotState(seqDecide); !reflect.DeepEqual(sp, ss) {
			t.Fatalf("frame %d %s: decide state diverged\nparallel   %+v\nsequential %+v", i, step.name, sp, ss)
		}

		ca, ok := parScore.contentArea(img)
		if !ok {
			t.Fatal("synthetic geometry")
		}
		hp := parScore.scoreAll(img, parScore.prepare(ca), at)
		hs := seqScore.scoreAll(img, seqScore.prepare(ca), at)
		if !reflect.DeepEqual(hp, hs) {
			t.Fatalf("frame %d %s: scores diverged\nparallel   %+v\nsequential %+v", i, step.name, hp, hs)
		}
		sp, ss := snapshotState(parScore), snapshotState(seqScore)
		if !reflect.DeepEqual(sp, ss) {
			t.Fatalf("frame %d %s: score state diverged\nparallel   %+v\nsequential %+v", i, step.name, sp, ss)
		}
		for _, profile := range []string{"camp", "duel"} {
			for _, c := range []*Catalog{parDecide, seqDecide} {
				_ = c.SupportsFight(img, profile)
			}
		}
		if sp, ss := snapshotState(parDecide), snapshotState(seqDecide); !reflect.DeepEqual(sp, ss) {
			t.Fatalf("frame %d %s: support state diverged", i, step.name)
		}

		if dp.Kind == engine.GateFight {
			fights++
		}
		if sp.coldFrames > 0 {
			skippedCold++
		}
		for _, h := range sp.hints {
			if h.valid {
				hinted++
				break
			}
		}
	}
	// Guard the fixture itself: the sequence must reach hot-fight cold skips.
	if fights == 0 || skippedCold == 0 || hinted == 0 {
		t.Fatalf("sequence lacks coverage: fights=%d skippedCold=%d hinted=%d", fights, skippedCold, hinted)
	}
}

type exclusiveMatcher struct {
	t        *testing.T
	inFlight chan struct{}
	calls    int
}

func (m *exclusiveMatcher) Match(q match.Query) (match.Score, error) {
	select {
	case m.inFlight <- struct{}{}:
	default:
		m.t.Error("injected matcher was called concurrently")
		return match.Score{}, nil
	}
	defer func() { <-m.inFlight }()
	m.calls++
	time.Sleep(50 * time.Microsecond) // Widen any overlap window.
	return match.NCC{}.Match(q)
}

func TestParallelScoringOnlyForPlainNCC(t *testing.T) {
	cat := embeddedCatalog(t)
	for _, tc := range []struct {
		name    string
		matcher match.Matcher
		want    bool
	}{
		{"NCC", match.NCC{}, true},
		{"pointer NCC", &match.NCC{}, false},
		{"wrapped NCC", serialNCC{}, false},
		{"sequence", &scoreSequence{}, false},
		{"recording", &recordingMatcher{}, false},
		{"nil", nil, false},
	} {
		cat.matcher = tc.matcher
		if got := cat.parallelScoring(); got != tc.want {
			t.Errorf("%s: parallelScoring = %v, want %v", tc.name, got, tc.want)
		}
	}

	// An injected matcher sees one call at a time on both cold and hinted frames.
	builder := embeddedCatalog(t)
	m := &exclusiveMatcher{t: t, inFlight: make(chan struct{}, 1)}
	cat = embeddedCatalog(t)
	cat.matcher = m
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		at = at.Add(33 * time.Millisecond)
		cat.DecideAt(synthFrame(t, builder, 1920, 1080, uint64(i), image.Point{}, "fight-scene-key", "raw-camp-key"), at)
	}
	if m.calls == 0 {
		t.Fatal("injected matcher was never used")
	}
}
