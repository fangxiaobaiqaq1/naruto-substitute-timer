package engine

import (
	"image"
	"image/color"
	"testing"
	"time"
)

func TestReviewProfileEvidenceMustNotConfirmAcrossCaptureOutage(t *testing.T) {
	gate := &supportingGate{d: GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: "duel"}}
	g := NewGated(gate, &hudInner{}).(TimedEngine)
	img := image.NewRGBA(image.Rect(0, 0, 800, 450))
	at := time.Unix(1700000000, 0)
	g.AnalyzeAt(img, at)
	gate.d.LayoutProfile = "camp"
	g.AnalyzeAt(img, at.Add(20*time.Millisecond))
	// Native capture errors never call AnalyzeAt; no intervening image evidence.
	img.SetRGBA(400, 200, color.RGBA{100, 110, 120, 255})
	got := g.AnalyzeAt(img, at.Add(5*time.Second))
	if got.LayoutProfile == "camp" && got.Fighting {
		t.Fatalf("two isolated markers across a 5s outage confirmed a sustained profile change: %+v", got)
	}
	if got.Scene != "fight" {
		t.Fatal("capture gap erased established context")
	}
	for i, ms := range []int{5100, 5200, 5320} {
		img.SetRGBA(400, 200, color.RGBA{uint8(i + 1), 110, 120, 255})
		got = g.AnalyzeAt(img, at.Add(time.Duration(ms)*time.Millisecond))
		if i < 2 && got.LayoutProfile == "camp" {
			t.Fatal("new candidate was not restarted after gap")
		}
	}
	if !got.Fighting || got.LayoutProfile != "camp" {
		t.Fatal("fresh sustained profile could not confirm")
	}
}

func TestMissingOrRepeatedTimestampBreaksProfileCandidate(t *testing.T) {
	for _, kind := range []string{"nil", "repeated timestamp"} {
		gate := &supportingGate{d: GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: "duel"}}
		g := NewGated(gate, &hudInner{}).(TimedEngine)
		img := image.NewRGBA(image.Rect(0, 0, 800, 450))
		at := time.Unix(1700000000, 0)
		g.AnalyzeAt(img, at)
		gate.d.LayoutProfile = "camp"
		for _, ms := range []int{20, 120, 220} {
			g.AnalyzeAt(img, at.Add(time.Duration(ms)*time.Millisecond))
		}
		if kind == "nil" {
			g.AnalyzeAt(nil, at.Add(240*time.Millisecond))
		} else {
			g.AnalyzeAt(img, at.Add(220*time.Millisecond))
		}
		img.SetRGBA(400, 200, color.RGBA{100, 110, 120, 255})
		if got := g.AnalyzeAt(img, at.Add(340*time.Millisecond)); got.LayoutProfile == "camp" {
			t.Fatalf("%s left old candidate alive: %+v", kind, got)
		}
	}
}
