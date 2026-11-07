package hudtext

import (
	"image"
	"image/draw"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/engine"
	"narutotimer/internal/match"
	"narutotimer/internal/ocr"
)

func TestVSFooterAccountRequiresCompleteConfiguredName(t *testing.T) {
	for _, tc := range []struct {
		raw  [variants]string
		want string
	}{
		{[variants]string{"白 方 小", "白方小", "自方小"}, "白方小"},
		{[variants]string{"白方小", "自方小", ""}, ""},
		{[variants]string{"白方小号", "白方小号", "白方小"}, ""},
		{[variants]string{"42白方小", "42白方小", "白方小"}, ""},
		{[variants]string{"70%", "70%", "70%"}, ""},
		{[variants]string{"忍界大师", "忍界大师", "忍界大师"}, ""},
		{[variants]string{"陌生账号", "陌生账号", "陌生账号"}, ""},
	} {
		if got := footerAccount(tc.raw, []string{"白方小"}); got != tc.want {
			t.Fatalf("%q: got %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func testVSFooter(t *testing.T, mineSide int) (*Engine, *fakeEngine, *fakeReader, *image.RGBA, []ocr.Line) {
	t.Helper()
	cfg := config.Default()
	cfg.UI.AutoTextRecognition = false // the optional footer is independent
	cfg.UI.PlayerNames = []string{"白方小"}
	inner := &fakeEngine{result: engine.Result{Scene: "vs", Uncertain: true, LayoutProfile: "camp"}}
	reader := &fakeReader{called: make(chan struct{}, 10), replies: make(chan reply, 10), closed: make(chan struct{})}
	e := New(inner, cfg, reader, nil)
	t.Cleanup(func() { e.Close() })
	img := image.NewRGBA(image.Rect(0, 0, 960, 540))
	regions, ok := nameRegions(img, cfg.Layout, "vs")
	if !ok {
		t.Fatal("no VS footer regions")
	}
	var strips [2]strip
	for i, roi := range regions {
		draw.Draw(img, roi, syntheticGlyphs(), image.Point{}, draw.Src)
		strips[i].img = match.CropRGBA(img, roi)
	}
	sheet := buildSheet(strips)
	var lines []ocr.Line
	for _, row := range sheet.rows {
		text := "不认识的对手"
		if row.side == mineSide || mineSide == 2 {
			text = "白方小"
		}
		lines = append(lines, ocr.Line{Words: []ocr.Word{{Text: text,
			X: float64(row.rect.Min.X + 2), Y: float64(row.rect.Min.Y + 2),
			Width: float64(row.rect.Dx() - 4), Height: float64(row.rect.Dy() - 4)}}})
	}
	return e, inner, reader, img, lines
}

func TestVSFooterModeBoundedConfirmationAndDisable(t *testing.T) {
	for _, side := range []int{0, 1, 2} {
		e, _, reader, img, lines := testVSFooter(t, side)
		at := time.Unix(1700000000, 0)
		if got := e.AnalyzeAt(img, at); got.PlayerSide != "" || len(e.jobs) != 0 || e.busy {
			t.Fatal("disabled-by-default footer submitted OCR")
		}
		e.SetPreMatchSideRecognition(true)
		if !e.NeedsAnalysis(at.Add(time.Millisecond)) {
			t.Fatal("enabling on identical pixels did not invalidate analysis cache")
		}
		for i := range 2 {
			now := at.Add(time.Duration(i)*500*time.Millisecond + time.Millisecond)
			if !e.NeedsAnalysis(now) {
				t.Fatal("frozen VS never schedules the next confirmation")
			}
			e.AnalyzeAt(img, now)
			awaitCall(t, reader)
			if e.NeedsAnalysis(now.Add(time.Millisecond)) {
				t.Fatal("busy OCR forces every identical frame to re-analyze")
			}
			reader.replies <- reply{lines: lines}
			awaitResult(t, e)
			if !e.NeedsAnalysis(now.Add(2 * time.Millisecond)) {
				t.Fatal("frozen VS cannot consume completed OCR")
			}
			got := e.AnalyzeAt(img, now.Add(2*time.Millisecond))
			want := ""
			if i == 1 && side < 2 {
				want = []string{"left", "right"}[side]
			}
			if got.PlayerSide != want || got.Fighting || !got.Uncertain || got.RoundOpening || got.Scene != "vs" || got.LeftNinja != "" || got.RightNinja != "" {
				t.Fatalf("side=%d frame=%d: %+v", side, i, got)
			}
		}
		e.SetPreMatchSideRecognition(false)
		if !e.NeedsAnalysis(at.Add(time.Second)) {
			t.Fatal("disabling did not invalidate cached side")
		}
		if got := e.AnalyzeAt(img, at.Add(time.Second)); got.PlayerSide != "" || got.PlayerName != "" || got.OppName != "" {
			t.Fatalf("disabled VS retained identity: %+v", got)
		}
	}
}

func TestVSFooterCannotRetainShortAccountAfterSuffixAppears(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		e, _, reader, img, lines := testVSFooter(t, 0)
		regions, _ := nameRegions(img, e.cfg.Layout, "vs")
		roi := regions[0]
		// Leave blank space after the original three synthetic glyphs. OCR's
		// old word box covers only that prefix, not a later appended glyph.
		draw.Draw(img, image.Rect(roi.Min.X+60, roi.Min.Y, roi.Max.X, roi.Max.Y), image.Black, image.Point{}, draw.Src)
		for i := 0; i < variants; i++ {
			w := &lines[i].Words[0]
			w.Width = (w.Width + 4) * 55 / float64(roi.Dx())
		}
		e.SetPreMatchSideRecognition(true)
		at := time.Unix(1700000000, 0)
		for i := 0; i < 2; i++ {
			now := at.Add(time.Duration(i) * 500 * time.Millisecond)
			e.AnalyzeAt(img, now)
			awaitCall(t, reader)
			if !accepted {
				break // change pixels before consuming the first pending result
			}
			reader.replies <- reply{lines: lines}
			awaitResult(t, e)
			e.AnalyzeAt(img, now.Add(time.Millisecond))
		}
		if accepted && e.sides[0].account.accepted != "白方小" {
			t.Fatal("prefix was not initially confirmed")
		}
		for y := 4; y < 20; y++ {
			for x := 66; x < 74; x++ {
				if (x+y)%3 == 0 {
					img.Set(roi.Min.X+x, roi.Min.Y+y, image.White.At(0, 0))
				}
			}
		}
		if !accepted {
			reader.replies <- reply{lines: lines}
			awaitResult(t, e)
		}
		got := e.AnalyzeAt(img, at.Add(700*time.Millisecond))
		if got.PlayerSide != "" || e.sides[0].account.candidate != "" || e.sides[0].account.accepted != "" {
			t.Fatalf("accepted=%v: appended suffix retained prefix: %+v", accepted, got)
		}
	}
}

func TestVSFooterLateResultsCannotCrossBoundary(t *testing.T) {
	for _, boundary := range []string{"off", "name", "scene", "seek", "pixels"} {
		t.Run(boundary, func(t *testing.T) {
			e, inner, reader, img, lines := testVSFooter(t, 0)
			e.SetPreMatchSideRecognition(true)
			at := time.Unix(1700000000, 0)
			e.AnalyzeAt(img, at)
			awaitCall(t, reader)
			next := at.Add(time.Millisecond)
			switch boundary {
			case "off":
				e.SetPreMatchSideRecognition(false)
			case "name":
				e.names = func() []string { return []string{"另一个账号"} }
			case "scene":
				inner.set(engine.Result{Scene: "lobby"})
			case "seek":
				next = at.Add(-time.Second)
			case "pixels":
				regions, _ := nameRegions(img, e.cfg.Layout, "vs")
				draw.Draw(img, regions[0], image.Black, image.Point{}, draw.Src)
			}
			reader.replies <- reply{lines: lines}
			awaitResult(t, e)
			if got := e.AnalyzeAt(img, next); got.PlayerSide != "" || e.sides[0].account.candidate != "" {
				t.Fatalf("late %s result acquired identity: %+v", boundary, got)
			}
		})
	}
}
