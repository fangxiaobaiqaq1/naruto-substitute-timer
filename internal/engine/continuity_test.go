package engine

import (
	"fmt"
	"image"
	"image/color"
	"testing"
	"time"
)

type supportingGate struct {
	d         GateDecision
	supported bool
}

func (g *supportingGate) Decide(*image.RGBA) GateDecision        { return g.d }
func (g *supportingGate) SupportsFight(*image.RGBA, string) bool { return g.supported }

type hudInner struct {
	profile    string
	unreadable bool
}

func (i *hudInner) Prefer(p string) { i.profile = p }
func (i *hudInner) Analyze(*image.RGBA) Result {
	r := Result{Name: "hud", LayoutProfile: i.profile}
	for _, side := range []string{"L", "R"} {
		for n := 1; n <= 4; n++ {
			r.Beads = append(r.Beads, BeadInfo{Label: fmt.Sprintf("%s%d", side, n), Conf: 1, Lit: n <= 2, Unknown: i.unreadable})
		}
	}
	return r
}

func TestFightContinuityNeedsCurrentBilateralEvidence(t *testing.T) {
	gate := &supportingGate{d: GateDecision{Kind: GateUncertain}, supported: true}
	inner := &hudInner{}
	g := NewGated(gate, inner).(TimedEngine)
	img := image.NewRGBA(image.Rect(0, 0, 800, 450))
	at := time.Unix(1700000000, 0)
	if r := g.AnalyzeAt(img, at); r.Fighting {
		t.Fatal("control and colored pixels established a new fight")
	}
	gate.d = GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: "duel", Confidence: .9}
	g.AnalyzeAt(img, at.Add(20*time.Millisecond))
	gate.d = GateDecision{Kind: GateUncertain}
	if r := g.AnalyzeAt(img, at.Add(40*time.Millisecond)); !r.Fighting || r.Uncertain || r.Scene != "fight" {
		t.Fatalf("fresh HUD did not bridge missed marker: %+v", r)
	}
	inner.unreadable = true
	if r := g.AnalyzeAt(img, at.Add(60*time.Millisecond)); r.Fighting || !r.Uncertain || r.Scene != "fight" {
		t.Fatalf("occlusion must keep context but stop votes: %+v", r)
	}
	if r := g.AnalyzeAt(img, at.Add(time.Second)); r.Fighting || !r.Uncertain || r.Scene != "fight" {
		t.Fatalf("context must persist without authorizing votes: %+v", r)
	}
}

func TestExplicitPageAndGeometryClearFightContinuity(t *testing.T) {
	for _, page := range []GateDecision{{Kind: GateNotFight, SceneID: "result"}, {Kind: GateUncertain, SceneID: "vs"}, {Kind: GateUncertain, SceneID: "unsupported-resolution"}} {
		gate := &supportingGate{d: GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: "duel"}, supported: true}
		g := NewGated(gate, &hudInner{}).(TimedEngine)
		img := image.NewRGBA(image.Rect(0, 0, 800, 450))
		at := time.Unix(1700000000, 0)
		g.AnalyzeAt(img, at)
		gate.d = page
		if r := g.AnalyzeAt(img, at.Add(20*time.Millisecond)); r.Fighting || r.Scene != page.SceneID {
			t.Fatalf("page hidden: %+v", r)
		}
		gate.d = GateDecision{Kind: GateUncertain}
		if r := g.AnalyzeAt(img, at.Add(40*time.Millisecond)); r.Fighting || r.Scene != "" {
			t.Fatalf("returned to stale match: %+v", r)
		}
		gate.d = GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: "duel"}
		g.AnalyzeAt(img, at.Add(60*time.Millisecond))
		gate.d = GateDecision{Kind: GateUncertain}
		if r := g.AnalyzeAt(image.NewRGBA(image.Rect(0, 0, 1280, 720)), at.Add(80*time.Millisecond)); r.Fighting || r.Scene != "" {
			t.Fatalf("geometry reused stale context: %+v", r)
		}
	}
}

func TestProfileSwitchNeedsDifferentImages(t *testing.T) {
	gate := &supportingGate{d: GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: "duel"}}
	inner := &hudInner{}
	g := NewGated(gate, inner).(TimedEngine)
	img := image.NewRGBA(image.Rect(0, 0, 800, 450))
	at := time.Unix(1700000000, 0)
	g.AnalyzeAt(img, at)
	gate.d.LayoutProfile = "camp"
	for _, ms := range []int{20, 40, 60} {
		if r := g.AnalyzeAt(img, at.Add(time.Duration(ms)*time.Millisecond)); r.Fighting || inner.profile != "duel" {
			t.Fatalf("single image changed layout: %+v", r)
		}
	}
	img.SetRGBA(400, 200, color.RGBA{100, 110, 120, 255})
	if r := g.AnalyzeAt(img, at.Add(80*time.Millisecond)); r.Fighting || r.LayoutProfile != "duel" {
		t.Fatalf("brief competing marker changed layout: %+v", r)
	}
	if r := g.AnalyzeAt(img, at.Add(340*time.Millisecond)); !r.Fighting || r.LayoutProfile != "camp" {
		t.Fatalf("new image did not confirm layout: %+v", r)
	}
}
