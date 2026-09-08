package hudtext

import (
	"image"
	"image/draw"
	"narutotimer/internal/engine"
	"testing"
	"time"
)

func TestReviewBriefSceneHoldMustNotDiscardUnchangedTextProof(t *testing.T) {
	e, inner, r, img, lines := testTextEngine(t)
	at := time.Unix(1700000000, 0)
	for i := 0; i < 2; i++ {
		now := at.Add(time.Duration(i) * 500 * time.Millisecond)
		e.AnalyzeAt(img, now)
		awaitCall(t, r)
		r.replies <- reply{lines: lines}
		awaitResult(t, e)
		got := e.AnalyzeAt(img, now.Add(time.Millisecond))
		if i == 1 && got.RightNinja != "山中井野" {
			t.Fatalf("setup: %+v", got)
		}
	}
	inner.set(engine.Result{Fighting: false, Uncertain: true, Scene: "fight", LayoutProfile: "duel"})
	e.AnalyzeAt(img, at.Add(520*time.Millisecond))
	inner.set(engine.Result{Fighting: true, Scene: "fight", LayoutProfile: "duel"})
	got := e.AnalyzeAt(img, at.Add(540*time.Millisecond))
	if got.RightNinja != "山中井野" || got.PlayerSide != "right" {
		t.Fatalf("unchanged name/account were discarded after a 20ms scene hold: %+v", got)
	}
}

func TestPausedAcceptedTextStillChecksCurrentPixelsAndBoundaries(t *testing.T) {
	for _, kind := range []string{"same pixels", "erased text", "long hold", "late resume", "vs", "geometry", "profile", "disabled", "seek"} {
		t.Run(kind, func(t *testing.T) {
			e, inner, r, img, lines := testTextEngine(t)
			at := time.Unix(1700000000, 0)
			for i := 0; i < 2; i++ {
				now := at.Add(time.Duration(i) * 500 * time.Millisecond)
				e.AnalyzeAt(img, now)
				awaitCall(t, r)
				r.replies <- reply{lines: lines}
				awaitResult(t, e)
				e.AnalyzeAt(img, now.Add(time.Millisecond))
			}
			inner.set(engine.Result{Uncertain: true, Scene: "fight", LayoutProfile: "duel"})
			if got := e.AnalyzeAt(img, at.Add(520*time.Millisecond)); got.RightNinja != "" || got.PlayerSide != "" {
				t.Fatal("paused evidence was emitted as current")
			}
			resume := at.Add(560 * time.Millisecond)
			switch kind {
			case "erased text":
				erased := cloneFrame(img)
				regions, _ := nameRegions(img, e.cfg.Layout, "duel")
				draw.Draw(erased, regions[1], image.Black, image.Point{}, draw.Src)
				e.AnalyzeAt(erased, at.Add(540*time.Millisecond))
			case "long hold":
				e.AnalyzeAt(img, at.Add(1500*time.Millisecond))
				e.AnalyzeAt(img, at.Add(2600*time.Millisecond))
				resume = at.Add(2620 * time.Millisecond)
			case "late resume":
				resume = at.Add(2600 * time.Millisecond)
			case "vs":
				inner.set(engine.Result{Uncertain: true, Scene: "vs", LayoutProfile: "duel"})
				e.AnalyzeAt(img, at.Add(540*time.Millisecond))
			case "geometry":
				e.AnalyzeAt(image.NewRGBA(image.Rect(0, 0, 960, 540)), at.Add(540*time.Millisecond))
			case "profile":
				inner.set(engine.Result{Uncertain: true, Scene: "fight", LayoutProfile: "camp"})
				e.AnalyzeAt(img, at.Add(540*time.Millisecond))
			case "disabled":
				e.SetEnabled(false)
				e.SetEnabled(true)
			case "seek":
				resume = at.Add(400 * time.Millisecond)
			}
			inner.set(engine.Result{Fighting: true, Scene: "fight", LayoutProfile: "duel"})
			got := e.AnalyzeAt(img, resume)
			if kind == "same pixels" {
				if got.RightNinja != "山中井野" || got.PlayerSide != "right" {
					t.Fatalf("unchanged fields lost: %+v", got)
				}
			} else if got.RightNinja != "" || got.PlayerSide != "" {
				t.Fatalf("old field crossed %s: %+v", kind, got)
			}
		})
	}
}

func TestPauseDiscardsTentativeAndLateOCRVotes(t *testing.T) {
	e, inner, r, img, lines := testTextEngine(t)
	at := time.Unix(1700000000, 0)
	e.AnalyzeAt(img, at)
	awaitCall(t, r)
	r.replies <- reply{lines: lines}
	awaitResult(t, e)
	e.AnalyzeAt(img, at.Add(time.Millisecond))
	e.AnalyzeAt(img, at.Add(500*time.Millisecond))
	awaitCall(t, r)
	inner.set(engine.Result{Uncertain: true, Scene: "fight", LayoutProfile: "duel"})
	e.AnalyzeAt(img, at.Add(520*time.Millisecond))
	inner.set(engine.Result{Fighting: true, Scene: "fight", LayoutProfile: "duel"})
	e.AnalyzeAt(img, at.Add(540*time.Millisecond))
	r.replies <- reply{lines: lines}
	awaitResult(t, e)
	if got := e.AnalyzeAt(img, at.Add(541*time.Millisecond)); got.RightNinja != "" {
		t.Fatal("late pre-pause result confirmed a name")
	}
	for i := 0; i < 2; i++ {
		now := at.Add(time.Second + time.Duration(i)*500*time.Millisecond)
		e.AnalyzeAt(img, now)
		awaitCall(t, r)
		r.replies <- reply{lines: lines}
		awaitResult(t, e)
		got := e.AnalyzeAt(img, now.Add(time.Millisecond))
		if i == 0 && got.RightNinja != "" {
			t.Fatal("tentative pre-pause vote survived")
		}
		if i == 1 && got.RightNinja != "山中井野" {
			t.Fatal("new independent results failed to confirm")
		}
	}
}

func TestPausedRecordedGlyphProofSurvivesOnlyUnrelatedBackdrop(t *testing.T) {
	e, inner, r, _, _ := testTextEngine(t)
	img, lines := recordedKankuro(t)
	at := time.Unix(1700000000, 0)
	for i := 0; i < 2; i++ {
		now := at.Add(time.Duration(i) * 500 * time.Millisecond)
		e.AnalyzeAt(img, now)
		awaitCall(t, r)
		r.replies <- reply{lines: lines}
		awaitResult(t, e)
		e.AnalyzeAt(img, now.Add(time.Millisecond))
	}
	proof := e.sides[1].ninjaProof
	if proof == nil {
		t.Fatal("fixture has no actual glyph proof")
	}
	changed := changeBackdrop(t, img, e.key.regions[1], proof.bounds)
	inner.set(engine.Result{Uncertain: true, Scene: "fight", LayoutProfile: "duel"})
	e.AnalyzeAt(changed, at.Add(520*time.Millisecond))
	inner.set(engine.Result{Fighting: true, Scene: "fight", LayoutProfile: "duel"})
	if got := e.AnalyzeAt(changed, at.Add(540*time.Millisecond)); got.RightNinja != "勘九郎" {
		t.Fatalf("unchanged glyphs lost on pause: %+v", got)
	}
	inner.set(engine.Result{Uncertain: true, Scene: "fight", LayoutProfile: "duel"})
	erased := cloneFrame(changed)
	draw.Draw(erased, proof.bounds, image.Black, image.Point{}, draw.Src)
	e.AnalyzeAt(erased, at.Add(560*time.Millisecond))
	inner.set(engine.Result{Fighting: true, Scene: "fight", LayoutProfile: "duel"})
	if got := e.AnalyzeAt(changed, at.Add(580*time.Millisecond)); got.RightNinja != "" {
		t.Fatal("erased/replaced glyphs restored from a stale proof")
	}
}
