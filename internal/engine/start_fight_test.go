package engine

import (
	"image"
	"testing"
)

type pairedControlGate struct {
	stubGate
	entry GateDecision
	calls int
}

func (g *pairedControlGate) StartFight(*image.RGBA, string) GateDecision {
	g.calls++
	return g.entry
}

type currentHUD struct{ result Result }

func (h currentHUD) Analyze(*image.RGBA) Result { return h.result }

func TestCoveredMarkerEntryNeedsCompleteLegalCurrentHUD(t *testing.T) {
	for _, name := range []string{"complete", "unknown", "missing slot", "six-slot gap", "low confidence", "illegal prefix", "duplicate label", "uncertain", "page", "blank", "different profile"} {
		t.Run(name, func(t *testing.T) {
			res := (&hudInner{profile: "duel"}).Analyze(nil)
			gate := &pairedControlGate{stubGate: stubGate{GateDecision{Kind: GateUncertain}}, entry: GateDecision{Kind: GateFight, SceneID: "fight", LayoutProfile: "duel", Confidence: .9}}
			switch name {
			case "unknown":
				res.Beads[0].Unknown = true
			case "missing slot":
				res.Beads = res.Beads[:7]
			case "six-slot gap":
				res.LeftSlots = 6
			case "low confidence":
				res.Beads[0].Conf = .59
			case "illegal prefix":
				res.Beads[0].Lit = false
			case "duplicate label":
				res.Beads[1].Label = "L1"
			case "uncertain":
				res.Uncertain = true
			case "page":
				gate.d.SceneID = "vs"
			case "blank":
				gate.d.Kind = GateBlank
			case "different profile":
				gate.entry.LayoutProfile = "camp"
			}
			g := NewGated(gate, currentHUD{res})
			got := g.Analyze(image.NewRGBA(image.Rect(0, 0, 800, 450)))
			if got.Fighting != (name == "complete") || got.RoundOpening {
				t.Fatalf("%s authorized an invalid entry: %+v", name, got)
			}
			if name != "complete" && name != "different profile" && gate.calls != 0 {
				t.Fatal("incomplete HUD or explicit page reached the entry fallback")
			}
		})
	}
}
