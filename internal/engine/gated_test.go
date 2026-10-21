package engine

import (
	"image"
	"runtime"
	"sync"
	"testing"
	"time"
)

type stubGate struct{ d GateDecision }

func (s stubGate) Decide(*image.RGBA) GateDecision { return s.d }

type stubInner struct {
	name  string
	beads int
	calls int
}

func (s *stubInner) Analyze(img *image.RGBA) Result {
	if img == nil {
		return Result{Name: s.name}
	}
	s.calls++
	res := Result{Name: s.name}
	for i := 0; i < s.beads; i++ {
		res.Beads = append(res.Beads, BeadInfo{Label: "L1"})
	}
	return res
}

func TestGatedUncertainStillSamplesBeads(t *testing.T) {
	inner := &stubInner{name: "rgb", beads: 4}
	g := NewGated(stubGate{d: GateDecision{Kind: GateUncertain, Confidence: 0.4}}, inner)
	res := g.Analyze(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if inner.calls != 1 || len(res.Beads) != 4 {
		t.Fatalf("uncertain must still sample beads, got %+v calls=%d", res, inner.calls)
	}
	if res.Fighting || !res.Uncertain {
		t.Fatalf("dark/unknown beads must not start a fight, got %+v", res)
	}

	inner.beads = 0
	inner.calls = 0
	lit := &stubLitInner{name: "rgb", lit: 3}
	g = NewGated(stubGate{d: GateDecision{Kind: GateUncertain, Confidence: 0.4}}, lit)
	res = g.Analyze(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if res.Fighting || !res.Uncertain || len(res.Beads) != 3 {
		t.Fatalf("colored lobby pixels must not establish a fight without scene evidence, got %+v", res)
	}
}

type stubLitInner struct {
	name string
	lit  int
}

func (s *stubLitInner) Analyze(img *image.RGBA) Result {
	if img == nil {
		return Result{Name: s.name}
	}
	res := Result{Name: s.name}
	for i := 0; i < s.lit; i++ {
		res.Beads = append(res.Beads, BeadInfo{Label: "L1", Lit: true})
	}
	return res
}

func TestGatedNamedHoldSceneDoesNotFight(t *testing.T) {
	inner := &stubInner{name: "rgb", beads: 4}
	g := NewGated(stubGate{d: GateDecision{Kind: GateUncertain, SceneID: "vs", Confidence: 0.9}}, inner)
	res := g.Analyze(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if res.Fighting || !res.Uncertain {
		t.Fatalf("named hold scene must freeze, got %+v", res)
	}
}

func TestGatedNotFightSkipsBeads(t *testing.T) {
	inner := &stubInner{name: "rgb", beads: 4}
	g := NewGated(stubGate{d: GateDecision{Kind: GateNotFight, SceneID: "lobby", Confidence: 0.9}}, inner)
	res := g.Analyze(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if res.Fighting || res.Uncertain || len(res.Beads) != 0 || inner.calls != 0 {
		t.Fatalf("not-fight must not sample, got %+v calls=%d", res, inner.calls)
	}
	if res.Scene != "lobby" {
		t.Fatalf("scene = %q", res.Scene)
	}
}

func TestGatedPropagatesVerifiedRoundOpening(t *testing.T) {
	inner := &stubInner{name: "rgb", beads: 4}
	g := NewGated(stubGate{d: GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: "duel", RoundOpening: true}}, inner)
	got := g.Analyze(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if !got.Fighting || !got.RoundOpening {
		t.Fatalf("round opening metadata was dropped by gate: %+v", got)
	}
}

func TestGatedFightSamplesAndMarksEmptyUncertain(t *testing.T) {
	inner := &stubInner{name: "rgb", beads: 0}
	g := NewGated(stubGate{d: GateDecision{Kind: GateFight, SceneID: "fight", Confidence: 0.91}}, inner)
	res := g.Analyze(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if !res.Fighting || !res.Uncertain || inner.calls != 1 {
		t.Fatalf("empty fight sample must be uncertain, got %+v calls=%d", res, inner.calls)
	}

	inner.beads = 4
	res = g.Analyze(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if !res.Fighting || res.Uncertain || len(res.Beads) != 4 {
		t.Fatalf("fight with beads: %+v", res)
	}
}

func TestNewGatedNilGateReturnsInner(t *testing.T) {
	inner := &stubInner{name: "rgb"}
	g := NewGated(nil, inner)
	if g != inner {
		t.Fatalf("nil gate should return inner")
	}
}

type mutableGate struct{ d GateDecision }

func (g *mutableGate) Decide(*image.RGBA) GateDecision { return g.d }

type profileInner struct {
	mu      sync.Mutex
	prefer  string
	changes int
}

func (p *profileInner) Prefer(profile string) {
	p.mu.Lock()
	p.prefer = profile
	p.changes++
	p.mu.Unlock()
	// A concurrent frame could otherwise replace the preference before Analyze.
	runtime.Gosched()
}

func (p *profileInner) Analyze(img *image.RGBA) Result {
	if img == nil {
		return Result{Name: "profile"}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return Result{Name: "profile-" + p.prefer, Beads: []BeadInfo{{Label: "L1"}}}
}

func TestGatedBindsOnlyConfirmedProfileAndClearsEndScene(t *testing.T) {
	gate := &mutableGate{}
	inner := &profileInner{}
	g := NewGated(gate, inner)
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for _, tc := range []struct {
		decision GateDecision
		want     string
		changes  int
	}{
		{GateDecision{Kind: GateFight, LayoutProfile: "camp"}, "camp", 1},
		{GateDecision{Kind: GateUncertain}, "camp", 1},
		{GateDecision{Kind: GateBlank, LayoutProfile: "duel"}, "camp", 1},
		{GateDecision{Kind: GateFight, LayoutProfile: "duel"}, "duel", 2},
		{GateDecision{Kind: GateNotFight, SceneID: "lobby", LayoutProfile: "camp"}, "", 3},
	} {
		gate.d = tc.decision
		res := g.Analyze(img)
		if inner.prefer != tc.want || inner.changes != tc.changes {
			t.Fatalf("decision %+v: prefer %q changes %d, want %q changes %d", tc.decision, inner.prefer, inner.changes, tc.want, tc.changes)
		}
		if tc.decision.Kind == GateFight && res.Name != "profile-"+tc.want+"+gate" {
			t.Fatalf("fight frame sampled wrong profile: %+v", res)
		}
		if tc.decision.Kind == GateUncertain || tc.decision.Kind == GateBlank {
			if res.Fighting || !res.Uncertain {
				t.Fatalf("hold frame must not start a timer: %+v", res)
			}
		}
	}
}

type imageProfileGate struct{}

func (imageProfileGate) Decide(img *image.RGBA) GateDecision {
	profile := "camp"
	if img.Bounds().Dx() == 9 {
		profile = "duel"
	}
	return GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: profile}
}

func TestGatedConcurrentFramesCannotExchangeProfiles(t *testing.T) {
	g := NewGated(imageProfileGate{}, &profileInner{})
	var wg sync.WaitGroup
	for _, tc := range []struct {
		w       int
		profile string
	}{{8, "camp"}, {9, "duel"}} {
		for worker := 0; worker < 4; worker++ {
			wg.Add(1)
			go func(w int, profile string) {
				defer wg.Done()
				img := image.NewRGBA(image.Rect(0, 0, w, 8))
				for i := 0; i < 50; i++ {
					got := g.Analyze(img)
					if got.Name != "profile-"+profile+"+gate" {
						t.Errorf("%s frame sampled another frame's profile: %+v", profile, got)
						return
					}
				}
			}(tc.w, tc.profile)
		}
	}
	wg.Wait()
}

func TestGatedProfileSwitchNeedsTwoDistinctFrames(t *testing.T) {
	var hashes int
	pixelHashHook = func() { hashes++ }
	defer func() { pixelHashHook = nil }()
	gate := &mutableGate{d: GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: "camp"}}
	g := NewGated(gate, &profileInner{})
	start := time.Unix(1000, 0)
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	analyze := func(frame *image.RGBA, offset time.Duration) Result {
		return g.(TimedEngine).AnalyzeAt(frame, start.Add(offset))
	}
	if res := analyze(img, 0); !res.Fighting {
		t.Fatalf("camp fight not established: %+v", res)
	}
	if hashes != 0 {
		t.Fatalf("steady profile must not hash, got %d", hashes)
	}
	gate.d.LayoutProfile = "duel"
	step := 100 * time.Millisecond
	// The same pixels repeated past the confirmation window never confirm.
	for i := 1; i <= 6; i++ {
		res := analyze(img, time.Duration(i)*step)
		if res.Fighting || g.(*Gated).lastFight.LayoutProfile != "camp" {
			t.Fatalf("identical frame %d confirmed switch: %+v", i, res)
		}
	}
	// Hash once for the pending candidate, then only after 300ms has elapsed.
	if hashes != 1+3 {
		t.Fatalf("hash count = %d, want 4", hashes)
	}
	other := image.NewRGBA(image.Rect(0, 0, 8, 8))
	other.Pix[0] = 1
	if res := analyze(other, 7*step); !res.Fighting || g.(*Gated).lastFight.LayoutProfile != "duel" {
		t.Fatalf("distinct frame after window must confirm: %+v", res)
	}
}

func TestGatedProfileSwitchDistinctFrameTooEarly(t *testing.T) {
	gate := &mutableGate{d: GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: "camp"}}
	g := NewGated(gate, &profileInner{}).(*Gated)
	start := time.Unix(1000, 0)
	a := image.NewRGBA(image.Rect(0, 0, 8, 8))
	b := image.NewRGBA(image.Rect(0, 0, 8, 8))
	b.Pix[0] = 1
	g.AnalyzeAt(a, start)
	gate.d.LayoutProfile = "duel"
	g.AnalyzeAt(a, start.Add(100*time.Millisecond))
	if res := g.AnalyzeAt(b, start.Add(200*time.Millisecond)); res.Fighting || g.lastFight.LayoutProfile != "camp" {
		t.Fatalf("distinct frame inside window confirmed switch: %+v", res)
	}
}
