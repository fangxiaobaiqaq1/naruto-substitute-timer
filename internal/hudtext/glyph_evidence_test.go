package hudtext

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/engine"
	"narutotimer/internal/match"
	"narutotimer/internal/ocr"
)

func evidenceJob(t testing.TB, img *image.RGBA) job {
	t.Helper()
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	regions, ok := nameRegions(img, cfg.Layout, "duel")
	if !ok {
		t.Fatal("no name regions")
	}
	j := job{key: frameKey{bounds: img.Bounds(), regions: regions, profile: "duel"}}
	for i, roi := range regions {
		sig, white := lettering(img, roi)
		j.strips[i] = strip{img: match.CropRGBA(img, roi), signature: sig, white: white}
	}
	return j
}

func recordedKankuro(t testing.TB) (*image.RGBA, []ocr.Line) {
	t.Helper()
	img := realFrame(t, "../../inbox/regressions/duel-kankuro-text-stability-20260907.png")
	data, err := os.ReadFile("../../inbox/regressions/duel-kankuro-ocr-words-20260907.json")
	if err != nil {
		t.Fatal(err)
	}
	var lines []ocr.Line
	if err := json.Unmarshal(data, &lines); err != nil {
		t.Fatal(err)
	}
	return img, lines
}

func TestRecordedGlyphEvidenceToleratesBackdropNotChangedName(t *testing.T) {
	img, lines := recordedKankuro(t)
	j := evidenceJob(t, img)
	proof := buildSheet(j.strips).evidence(lines, j, 1, "勘九郎")
	if proof == nil || !proof.matches(img) {
		t.Fatal("missing current glyph proof")
	}
	if !proof.matches(changeBackdrop(t, img, j.key.regions[1], proof.bounds)) {
		t.Fatal("background invalidated name")
	}
	brighter := cloneFrame(img)
	for y := proof.bounds.Min.Y; y < proof.bounds.Max.Y; y++ {
		for x := proof.bounds.Min.X; x < proof.bounds.Max.X; x++ {
			c := brighter.RGBAAt(x, y)
			c.R = uint8(min(255, int(c.R)+8))
			c.G = uint8(min(255, int(c.G)+8))
			c.B = uint8(min(255, int(c.B)+8))
			brighter.SetRGBA(x, y, c)
		}
	}
	if !proof.matches(brighter) {
		t.Fatal("small uniform luma change invalidated name")
	}
	changed := cloneFrame(img)
	box := image.Rect(proof.bounds.Min.X, proof.bounds.Min.Y, proof.bounds.Min.X+16, proof.bounds.Max.Y)
	draw.Draw(changed, box, image.NewUniform(color.Black), image.Point{}, draw.Src)
	if proof.matches(changed) {
		t.Fatal("one erased glyph survived other matching glyphs")
	}
	draw.Draw(changed, proof.bounds, image.NewUniform(color.Black), image.Point{}, draw.Src)
	if proof.matches(changed) {
		t.Fatal("blank name survived")
	}
}

func TestRecordedNameAcquisitionAndRetriesIgnoreUnrelatedBackdrop(t *testing.T) {
	e, _, r, _, _ := testTextEngine(t)
	img, lines := recordedKankuro(t)
	j := evidenceJob(t, img)
	proof := buildSheet(j.strips).evidence(lines, j, 1, "勘九郎")
	if proof == nil {
		t.Fatal("no actual word geometry")
	}
	changed := changeBackdrop(t, img, j.key.regions[1], proof.bounds)
	at := time.Unix(1700000000, 0)
	for i := range 2 {
		now := at.Add(time.Duration(i) * 500 * time.Millisecond)
		e.AnalyzeAt(img, now)
		awaitCall(t, r)
		// A different backdrop is already on screen BEFORE OCR finishes.
		e.AnalyzeAt(changed, now.Add(10*time.Millisecond))
		r.replies <- reply{lines: lines}
		awaitResult(t, e)
		got := e.AnalyzeAt(changed, now.Add(20*time.Millisecond))
		if i == 0 && got.RightNinja != "" {
			t.Fatal("only one capture confirmed a name")
		}
		if i == 1 && got.RightNinja != "勘九郎" {
			t.Fatalf("valid late field discarded by backdrop: %+v", got)
		}
	}
	// A left-side retry reads nothing on the right. Existing right-side pixels
	// are still checked every frame; an empty OCR vote must not erase them.
	e.AnalyzeAt(img, at.Add(time.Second))
	awaitCall(t, r)
	r.replies <- reply{}
	awaitResult(t, e)
	for i := range 120 {
		frame := img
		if i%2 == 0 {
			frame = changed
		}
		got := e.AnalyzeAt(frame, at.Add(1001*time.Millisecond+time.Duration(i)*16*time.Millisecond))
		if got.RightNinja != "勘九郎" {
			t.Fatalf("name blinked at frame %d: %+v", i, got)
		}
	}
	// A real change is NOT hidden by retention, even on the very next frame.
	erased := cloneFrame(img)
	draw.Draw(erased, proof.bounds, image.NewUniform(color.Black), image.Point{}, draw.Src)
	if got := e.AnalyzeAt(erased, at.Add(3*time.Second)); got.RightNinja != "" {
		t.Fatalf("old identity survived changed lettering: %+v", got)
	}
}

func TestProvenNameClearsOnSceneGeometryDisableAndTimeReset(t *testing.T) {
	for _, mode := range []string{"scene", "uncertain", "geometry", "disabled", "seek"} {
		t.Run(mode, func(t *testing.T) {
			e, inner, r, _, _ := testTextEngine(t)
			img, lines := recordedKankuro(t)
			at := time.Unix(1700000000, 0)
			for i := range 2 {
				now := at.Add(time.Duration(i) * 500 * time.Millisecond)
				e.AnalyzeAt(img, now)
				awaitCall(t, r)
				r.replies <- reply{lines: lines}
				awaitResult(t, e)
				got := e.AnalyzeAt(img, now.Add(time.Millisecond))
				if i == 1 && got.RightNinja != "勘九郎" {
					t.Fatal("no proven name to invalidate")
				}
			}
			next := at.Add(time.Second)
			switch mode {
			case "scene":
				inner.set(engine.Result{Scene: "lobby"})
			case "uncertain":
				inner.set(engine.Result{Fighting: true, Uncertain: true, LayoutProfile: "duel"})
			case "geometry":
				img = image.NewRGBA(image.Rect(0, 0, 960, 540))
			case "disabled":
				e.SetEnabled(false)
			case "seek":
				next = at.Add(-time.Second)
			}
			if got := e.AnalyzeAt(img, next); got.RightNinja != "" {
				t.Fatalf("proven name survived %s", mode)
			}
		})
	}
}

func TestProofCoordinatesFollowFrameOrigin(t *testing.T) {
	img, lines := recordedKankuro(t)
	offset := image.Pt(37, 53)
	shifted := image.NewRGBA(img.Bounds().Add(offset))
	draw.Draw(shifted, shifted.Bounds(), img, img.Bounds().Min, draw.Src)
	j := evidenceJob(t, shifted)
	proof := buildSheet(j.strips).evidence(lines, j, 1, "勘九郎")
	if proof == nil || !proof.matches(shifted) {
		t.Fatal("lost non-zero frame origin")
	}
	if proof.matches(img) {
		t.Fatal("proof coordinates ignored frame origin")
	}
}

func TestKnownNamesGetBoundedVersionRefresh(t *testing.T) {
	e, inner, r, _, _ := testTextEngine(t)
	img, lines := recordedKankuro(t)
	inner.set(engine.Result{Fighting: true, LayoutProfile: "duel", LeftNinja: "模板忍者", PlayerSide: "left"})
	at := time.Unix(1700000000, 0)
	for i := range 2 {
		now := at.Add(time.Duration(i) * 500 * time.Millisecond)
		e.AnalyzeAt(img, now)
		awaitCall(t, r)
		r.replies <- reply{lines: lines}
		awaitResult(t, e)
		e.AnalyzeAt(img, now.Add(time.Millisecond))
	}
	e.AnalyzeAt(img, at.Add(time.Second))
	awaitCall(t, r)
	r.replies <- reply{}
	awaitResult(t, e)
	for i := range 10 {
		got := e.AnalyzeAt(img, at.Add(1100*time.Millisecond+time.Duration(i)*100*time.Millisecond))
		if got.RightNinja != "勘九郎" {
			t.Fatal("background refresh erased name")
		}
	}
	if len(r.called) != 0 {
		t.Fatal("fully known names refreshed at acquisition frequency")
	}
	e.AnalyzeAt(img, at.Add(3*time.Second))
	awaitCall(t, r)
}

func TestFieldEvidenceRejectsMalformedWordGeometry(t *testing.T) {
	img, lines := recordedKankuro(t)
	j := evidenceJob(t, img)
	s := buildSheet(j.strips)
	for _, invalid := range []float64{math.NaN(), math.Inf(1), -1, 99999} {
		var bad []ocr.Line
		for _, line := range lines {
			copyLine := line
			copyLine.Words = append([]ocr.Word(nil), line.Words...)
			for i := range copyLine.Words {
				copyLine.Words[i].X = invalid
			}
			bad = append(bad, copyLine)
		}
		if s.evidence(bad, j, 1, "勘九郎") != nil {
			t.Fatalf("accepted invalid word coordinates: %v", invalid)
		}
	}
}

func TestFullAccountEvidenceDoesNotBindPrefix(t *testing.T) {
	img, lines := recordedKankuro(t)
	j := evidenceJob(t, img)
	s := buildSheet(j.strips)
	if s.evidence(lines, j, 1, "(等风)") != nil {
		t.Fatal("short account prefix bound to longer account")
	}
}

// Synthetic glyphs test lifecycle mechanics, not real OCR accuracy or a
// character-specific image template. Each block has a different stroke mask.
func syntheticGlyphs() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 200, 24))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	for k := range 10 {
		for y := 4; y < 20; y++ {
			for x := 3; x < 17; x++ {
				if (x+y+k)%5 < 2 {
					img.SetRGBA(k*20+x, y, color.RGBA{245, 245, 245, 255})
				}
			}
		}
	}
	return img
}

func TestBoundBaseCanUpgradeOnlyWithTwoCurrentFullVersionResults(t *testing.T) {
	img := syntheticGlyphs()
	base := newGlyphEvidence(img, image.Rect(0, 0, 60, 24))
	full := newGlyphEvidence(img, img.Bounds())
	if base == nil || full == nil {
		t.Fatal("synthetic glyph proof missing")
	}
	at := time.Unix(1700000000, 0)
	s := valueState{}
	var proof *glyphEvidence
	for i := range 2 {
		observeBound(&s, &proof, "照美冥", base, img, at.Add(time.Duration(i)*time.Second), true)
	}
	if s.accepted != "照美冥" {
		t.Fatal("base not acquired")
	}
	observeBound(&s, &proof, "照美冥[五代目水影]", full, img, at.Add(2*time.Second), false)
	if s.accepted != "照美冥" {
		t.Fatal("one full-version result upgraded special rules")
	}
	observeBound(&s, &proof, "照美冥[五代目水影]", full, img, at.Add(2*time.Second), false)
	if s.accepted != "照美冥" {
		t.Fatal("duplicate capture upgraded version")
	}
	observeBound(&s, &proof, "照美冥[五代目水影]", full, img, at.Add(3*time.Second), false)
	if s.accepted != "照美冥[五代目水影]" || proof != full {
		t.Fatal("stable base blocked independently verified version")
	}
	for _, value := range []string{"", "照美冥", "其他忍者"} {
		observeBound(&s, &proof, value, nil, img, at.Add(4*time.Second), false)
		if s.accepted != "照美冥[五代目水影]" {
			t.Fatal("noisy OCR erased still-matching full version")
		}
	}
	// Erase a single suffix block, not the whole long name.
	changed := cloneFrame(img)
	draw.Draw(changed, image.Rect(160, 0, 180, 24), image.NewUniform(color.Black), image.Point{}, draw.Src)
	if !base.matches(changed) || full.matches(changed) {
		t.Fatal("full version was validated by unchanged base alone")
	}
	var fresh valueState
	var freshProof *glyphEvidence
	observeBound(&fresh, &freshProof, "照美冥[五代目水影]", full, changed, at, true)
	if fresh.candidate != "" {
		t.Fatal("mismatched proof fell back to whole-strip signature")
	}
}

func BenchmarkCurrentGlyphEvidence(b *testing.B) {
	img, lines := recordedKankuro(b)
	j := evidenceJob(b, img)
	proof := buildSheet(j.strips).evidence(lines, j, 1, "勘九郎")
	if proof == nil {
		b.Fatal("missing proof")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if !proof.matches(img) {
			b.Fatal("current name lost")
		}
	}
}

func cloneFrame(img *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(img.Bounds())
	draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Src)
	return dst
}

// An outlined white mark outside the actual name but inside the search strip
// reproduces background-induced signature changes, without editing any glyph.
func changeBackdrop(t testing.TB, img *image.RGBA, roi, name image.Rectangle) *image.RGBA {
	t.Helper()
	out := cloneFrame(img)
	box := image.Rect(roi.Min.X+8, roi.Min.Y+3, roi.Min.X+20, roi.Min.Y+15)
	if !box.Intersect(name).Empty() || !box.In(roi) {
		t.Fatal("bad nuisance patch")
	}
	draw.Draw(out, box, image.NewUniform(color.RGBA{0, 0, 0, 255}), image.Point{}, draw.Src)
	draw.Draw(out, box.Inset(2), image.NewUniform(color.RGBA{255, 255, 255, 255}), image.Point{}, draw.Src)
	a, _ := lettering(img, roi)
	b, _ := lettering(out, roi)
	if a == b {
		t.Fatal("patch failed to change the old whole-strip signature")
	}
	return out
}

func TestInstalledOCRFieldGeometry(t *testing.T) {
	if os.Getenv("TIMER_TEST_SYSTEM_OCR") != "1" {
		t.Skip("opt-in Windows OCR integration")
	}
	img := realFrame(t, "../../inbox/regressions/duel-kankuro-text-stability-20260907.png")
	j := evidenceJob(t, img)
	s := buildSheet(j.strips)
	r := ocr.NewSystem()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	lines, err := r.Read(ctx, s.image)
	if err != nil {
		t.Fatal(err)
	}
	title := NewDictionary("").consensus(s.texts(lines)[1])
	if title.Ninja != "勘九郎" {
		t.Fatalf("real OCR: %+v %+v", title, s.texts(lines))
	}
	proof := s.evidence(lines, j, 1, title.Ninja)
	if proof == nil || !proof.matches(img) {
		t.Fatalf("real OCR word coordinates did not bind: %+v", lines)
	}
	t.Logf("actual OCR ninja=%s bounds=%v cells=%d", title.Ninja, proof.bounds, len(proof.cells))
	changed := changeBackdrop(t, img, j.key.regions[1], proof.bounds)
	if !proof.matches(changed) {
		t.Fatal("unrelated background erased live glyph evidence")
	}
	if output := os.Getenv("TIMER_OCR_WORD_REPORT"); output != "" {
		data, _ := json.MarshalIndent(lines, "", "  ")
		if err := os.WriteFile(output, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstalledOCRTayuyaHasBaseNotInventedVersion(t *testing.T) {
	if os.Getenv("TIMER_TEST_SYSTEM_OCR") != "1" {
		t.Skip("opt-in Windows OCR integration")
	}
	img := realFrame(t, "../../inbox/regressions/duel-tayuya-text-stability-20260907.png")
	j := evidenceJob(t, img)
	s := buildSheet(j.strips)
	r := ocr.NewSystem()
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	lines, err := r.Read(ctx, s.image)
	if err != nil {
		t.Fatal(err)
	}
	raw := s.texts(lines)[1]
	title := NewDictionary("").consensus(raw)
	if title.Ninja != "多由也" {
		t.Fatalf("actual OCR baseline: %q -> %+v", raw, title)
	}
	proof := s.evidence(lines, j, 1, title.Ninja)
	if proof == nil || !proof.matches(img) {
		t.Fatal("base OCR did not bind actual word pixels")
	}
	if !proof.matches(changeBackdrop(t, img, j.key.regions[1], proof.bounds)) {
		t.Fatal("background erased base")
	}
	t.Logf("actual OCR=%q -> %+v; only base verified, full version still unresolved", raw, title)
}

func TestInstalledOCRLiveNameSurvivesChangingBackdrop(t *testing.T) {
	if os.Getenv("TIMER_TEST_SYSTEM_OCR") != "1" {
		t.Skip("opt-in Windows OCR integration")
	}
	img, lines := recordedKankuro(t)
	j := evidenceJob(t, img)
	proof := buildSheet(j.strips).evidence(lines, j, 1, "勘九郎")
	if proof == nil {
		t.Fatal("missing recorded proof")
	}
	changed := changeBackdrop(t, img, j.key.regions[1], proof.bounds)
	cfg, err := config.Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.AutoTextRecognition = true
	inner := &fakeEngine{result: engine.Result{Fighting: true, LayoutProfile: "duel"}}
	s := buildSheet(j.strips)
	r := &evidenceAuditReader{Recognizer: ocr.NewSystem(), after: func(lines []ocr.Line) {
		raw := s.texts(lines)[1]
		t.Logf("right treatments: %q -> %+v", raw, NewDictionary("").consensus(raw))
	}}
	e := New(inner, cfg, r, nil)
	defer e.Close()
	start, first := time.Now(), time.Time{}
	maxAnalyze := time.Duration(0)
	frames := 0
	for time.Since(start) < 8*time.Second {
		frame := img
		if frames%2 == 0 {
			frame = changed
		}
		began := time.Now()
		got := e.AnalyzeAt(frame, began)
		maxAnalyze = max(maxAnalyze, time.Since(began))
		frames++
		if got.RightNinja == "勘九郎" {
			if first.IsZero() {
				first = time.Now()
			}
		} else if !first.IsZero() {
			t.Fatalf("real OCR identity blinked after %v: %+v", time.Since(first), got)
		}
		if !first.IsZero() && time.Since(first) > 3*time.Second {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if first.IsZero() {
		t.Fatal("never acquired actual name while unrelated backdrop changed")
	}
	e.mu.Lock()
	haveProof := e.sides[1].ninjaProof != nil
	e.mu.Unlock()
	if !haveProof {
		t.Fatal("test used whole-strip hash rather than actual word evidence")
	}
	t.Logf("real OCR acquisition=%s, stable frames=%d, max Analyze=%s (offline, not live game latency)", first.Sub(start), frames, maxAnalyze)
}

type evidenceAuditReader struct {
	ocr.Recognizer
	after func([]ocr.Line)
}

func (r *evidenceAuditReader) Read(ctx context.Context, img image.Image) ([]ocr.Line, error) {
	lines, err := r.Recognizer.Read(ctx, img)
	if err == nil {
		r.after(lines)
	}
	return lines, err
}
