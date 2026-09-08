package hudtext

import (
	"image"
	"image/draw"
	"testing"
	"time"
)

func TestTableCandidateNeverBecomesConfirmedOrSpecial(t *testing.T) {
	e, _, reader, img, lines := testTextEngine(t)
	for i := range lines {
		lines[i].Text = "漩涡呜人[暴怒·第六尾]"
		lines[i].Words[0].Text = lines[i].Text
	}
	at := time.Unix(1700000000, 0)
	for i := range 2 {
		start := at.Add(time.Duration(i) * 500 * time.Millisecond)
		e.AnalyzeAt(img, start)
		awaitCall(t, reader)
		reader.replies <- reply{lines: lines}
		awaitResult(t, e)
		got := e.AnalyzeAt(img, start.Add(time.Millisecond))
		if got.LeftNinja != "" || got.RightNinja != "" || got.PlayerSide != "" || got.LeftSlots != 0 || got.RightSlots != 0 {
			t.Fatalf("candidate became rule/identity: %+v", got)
		}
		if i == 0 && got.RightNinjaCandidate != "" {
			t.Fatal("single frame candidate escaped")
		}
		if i == 1 && got.RightNinjaCandidate != "漩涡鸣人" {
			t.Fatalf("candidate missing: %+v", got)
		}
	}
	regions, _ := nameRegions(img, e.cfg.Layout, "duel")
	draw.Draw(img, regions[1], image.Black, image.Point{}, draw.Src)
	if got := e.AnalyzeAt(img, at.Add(502*time.Millisecond)); got.RightNinjaCandidate != "" {
		t.Fatalf("stale candidate survived glyph change: %+v", got)
	}
	e.SetEnabled(false)
	if got := e.AnalyzeAt(img, at.Add(time.Second)); got.LeftNinjaCandidate != "" {
		t.Fatal("disabled OCR kept candidate")
	}
}
