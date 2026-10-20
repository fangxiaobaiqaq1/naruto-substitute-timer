package scene

import (
	"reflect"
	"testing"
	"time"

	"narutotimer/internal/engine"
)

var _ engine.FrameScopedGate = (*Catalog)(nil)

// Inside BeginFrame/EndFrame the gate shares gray views and full-ROI scores
// between DecideAt, SupportsFight, StartFight and ScoreAll. Every decision,
// score and hint/cold-scan state must equal the unscoped calls exactly.
func TestFrameMemoMatchesUnscopedCalls(t *testing.T) {
	builder := embeddedCatalog(t)
	scoped, plain := embeddedCatalog(t), embeddedCatalog(t)
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	reused := 0
	for i, step := range synthSequence() {
		at = at.Add(step.dt)
		img := synthFrame(t, builder, step.w, step.h, step.seed, step.offset, step.ids...)

		scoped.BeginFrame(img)
		ds := scoped.DecideAt(img, at)
		if memo := scoped.memo(img); memo != nil {
			reused += len(memo.full)
		}
		if other := synthFrame(t, builder, step.w, step.h, step.seed+1000, step.offset); scoped.memo(other) != nil {
			t.Fatal("memo used for a different image")
		}
		var ss [2]bool
		var fs [2]engine.GateDecision
		for k, profile := range []string{"camp", "duel"} {
			ss[k] = scoped.SupportsFight(img, profile)
			fs[k] = scoped.StartFight(img, profile)
		}
		hs := scoped.ScoreAll(img)
		scoped.EndFrame()
		if scoped.memo(img) != nil {
			t.Fatal("memo outlived EndFrame")
		}

		dp := plain.DecideAt(img, at)
		var sp [2]bool
		var fp [2]engine.GateDecision
		for k, profile := range []string{"camp", "duel"} {
			sp[k] = plain.SupportsFight(img, profile)
			fp[k] = plain.StartFight(img, profile)
		}
		hp := plain.ScoreAll(img)
		if ds != dp || ss != sp || fs != fp || !reflect.DeepEqual(hs, hp) {
			t.Fatalf("frame %d %s: scoped %+v %v %+v %+v\nplain %+v %v %+v %+v", i, step.name, ds, ss, fs, hs, dp, sp, fp, hp)
		}
		if a, b := snapshotState(scoped), snapshotState(plain); !reflect.DeepEqual(a, b) {
			t.Fatalf("frame %d %s: state diverged\nscoped %+v\nplain  %+v", i, step.name, a, b)
		}
	}
	if reused == 0 {
		t.Fatal("sequence never reused a full-ROI score")
	}
}
