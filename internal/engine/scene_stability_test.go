package engine

import (
	"image"
	"image/color"
	"testing"
	"time"
)

func TestOcclusionKeepsContextButNotVotesAndCanRevalidate(t *testing.T) {
	gate := &supportingGate{d: GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: "camp"}, supported: true}
	inner := &hudInner{}
	g := NewGated(gate, inner).(TimedEngine)
	img := image.NewRGBA(image.Rect(0, 0, 800, 450))
	at := time.Unix(1700000000, 0)
	g.AnalyzeAt(img, at)
	gate.d = GateDecision{Kind: GateBlank, SceneID: "blank"}
	inner.unreadable = true
	for _, ms := range []int{20, 800, 1600, 10000} {
		r := g.AnalyzeAt(img, at.Add(time.Duration(ms)*time.Millisecond))
		if r.Fighting || !r.Uncertain || r.Scene != "fight" {
			t.Fatalf("lost context or fabricated votes during obstruction: %+v", r)
		}
	}
	gate.d = GateDecision{Kind: GateUncertain}
	inner.unreadable = false
	if r := g.AnalyzeAt(img, at.Add(10020*time.Millisecond)); !r.Fighting || r.Uncertain || r.LayoutProfile != "camp" {
		t.Fatalf("current bilateral HUD/control could not revalidate: %+v", r)
	}
}

func TestCompetingMarkerCannotOverrideCurrentSupportedProfile(t *testing.T) {
	gate := &supportingGate{d: GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: "duel"}, supported: true}
	g := NewGated(gate, &hudInner{}).(TimedEngine)
	img := image.NewRGBA(image.Rect(0, 0, 800, 450))
	at := time.Unix(1700000000, 0)
	g.AnalyzeAt(img, at)
	gate.d.LayoutProfile = "camp"
	for i := 1; i <= 100; i++ {
		img.SetRGBA(400, 200, color.RGBA{uint8(i), 110, 120, 255})
		r := g.AnalyzeAt(img, at.Add(time.Duration(i)*20*time.Millisecond))
		if !r.Fighting || r.Uncertain || r.LayoutProfile != "duel" {
			t.Fatalf("fresh old-profile evidence overridden at %d: %+v", i, r)
		}
	}
}
