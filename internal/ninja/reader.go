package ninja

import (
	"bytes"
	"embed"
	"image"
	_ "image/png"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"narutotimer/internal/match"
)

// Name strips come from user-supplied HUD crops. Special strips establish the
// exact variant; ordinary strips establish only a display name (Slots == 0).
// Neither kind establishes the scene or the player's side. Only the strips
// NewReaderWithAvatars loads are embedded (TestEmbeddedNameTemplatesAllLoad);
// source crops and unused ordinary names stay in the tree for tests.
//
//go:embed templates/hashirama.png templates/hashirama_alt.png templates/madara.png
//go:embed templates/obito.png templates/obito_current.png templates/naruto.png templates/naruto_right.png
//go:embed templates/sasuke_xiayin.png templates/sasuke_xiayin_duel.png
//go:embed templates/itachi_hyakusen.png templates/itachi_hyakusen_mumu.png templates/itachi_hyakusen_portrait.png
//go:embed templates/minato_kyubi.png templates/hashirama_edo.png templates/naruto_student.png
var templates embed.FS

type Palette string

const (
	Warm   Palette = "warm" // Blue/orange six-slot variants.
	Purple Palette = "purple"
	Red    Palette = "red"
	Xiayin Palette = "xiayin" // Exact Sasuke variant supports purple and red HUD skins.
)

type Readout struct {
	Name string
	// TitleName and AvatarName preserve independent same-frame evidence for
	// diagnostics. Name is set only by ResolveEvidence after their agreement.
	TitleName   string
	AvatarName  string
	AvatarScore float64
	Slots       int
	Palette     Palette
	Score       float64
	// RowOffsetY shifts the calibrated bean row in 960x540 reference pixels.
	// It is geometry, not evidence of a current name or a readable bean.
	RowOffsetY float64
	// Unverified retains recently verified slot geometry during a short label
	// gap. Name/Palette/Score are empty; hints alone MUST NOT supply bean votes.
	Unverified bool
	// PaletteHint is a prior verified special-skin classifier hint. It is set
	// only while Unverified and never represents current name evidence.
	PaletteHint Palette
}
type nameTemplate struct {
	readout         Readout
	gray            *image.Gray
	portrait        *image.Gray // optional independent HUD portrait evidence
	requirePortrait bool
}
type scaledName struct {
	readout         Readout
	ncc             *match.PreparedNCC // nominal size round(src·scale)
	size            image.Point
	variants        *fineVariants // nearby sizes, prepared on first use
	coarse          *match.PreparedNCC
	portrait        *match.PreparedNCC
	portraitSize    image.Point
	requirePortrait bool
}
type Reader struct {
	mu     sync.Mutex
	source []nameTemplate
	// scales caches the prepared templates per HUD scale, most recent first.
	// Both sides (and camp/duel) may use different scales on every frame,
	// e.g. after a hand-drawn calibration; one shared slot was rebuilt for
	// each of them every frame.
	scales  []readerScale
	avatars *avatarCatalog
}

// readerScale is the immutable prepared template set for one exact scale.
type readerScale struct {
	scale    float64
	prepared []scaledName
}

// readerScales bounds the scale cache: two sides times two profiles.
const readerScales = 4

// fineTemplate is one prepared size of a title strip.
type fineTemplate struct {
	ncc  *match.PreparedNCC
	size image.Point
}

// fineFactors are the strip sizes the fine stage scores, relative to the
// nominal round(src·scale): the nominal first, then a fixed order. A rendered
// title can be a percent or two off its strip times the bead-derived HUD scale
// (strip crop, resampling, phone aspect ratios): 鸣人[暴怒·第六尾] fits best at
// +1%, and at the nominal size alone it hovered around the .80 threshold and
// dropped out at many capture scales. Every size keeps the same threshold and
// margin; a template scores the max over its sizes (earliest size on ties).
// ±2% in 1% steps covers what the unchanged coarse locator admits (beyond
// about ±2.5% it drops strips anyway) with at most 0.5% left over.
var fineFactors = []float64{1, .99, 1.01, .98, 1.02}

// fineVariants are a strip's other fineFactors sizes at one scale, part of
// that scale's cached set. They are prepared on first use: only strips whose
// coarse locate passes ever need them, so the cache stays near its old size.
type fineVariants struct {
	once    sync.Once
	src     *image.Gray
	scale   float64
	nominal image.Point
	list    []fineTemplate
}

// get returns the distinct sizes in fineFactors order, skipping sizes below
// the 8px minimum and sizes already listed (the nominal included): at small
// scales several factors round to the same pixels.
func (v *fineVariants) get() []fineTemplate {
	if v == nil {
		return nil
	}
	v.once.Do(func() {
		seen := []image.Point{v.nominal}
		for _, f := range fineFactors[1:] {
			size := image.Pt(int(math.Round(float64(v.src.Bounds().Dx())*v.scale*f)), int(math.Round(float64(v.src.Bounds().Dy())*v.scale*f)))
			if size.X < 8 || size.Y < 8 || slices.Contains(seen, size) {
				continue
			}
			seen = append(seen, size)
			v.list = append(v.list, fineTemplate{ncc: match.PrepareNCC(match.ScaleGray(v.src, size.X, size.Y), nil), size: size})
		}
	})
	return v.list
}

func NewReader() *Reader { return NewReaderWithAvatars(AvatarOptions{}) }

// NewReaderWithAvatars uses the embedded A/S catalog by default. A caller can
// point to a reviewed external catalog for development; an invalid optional
// catalog falls back to embedded assets rather than degrading normal operation.
func NewReaderWithAvatars(options AvatarOptions) *Reader {
	r := &Reader{}
	r.avatars, _ = loadEmbeddedAvatarCatalog()
	if external, err := LoadAvatarCatalog(options); err == nil && external != nil {
		r.avatars = external
	}
	for _, spec := range []struct {
		file, name      string
		slots           int
		palette         Palette
		rowOffsetY      float64
		requirePortrait bool
	}{
		{"hashirama", Hashirama, 6, Warm, 0, false}, {"hashirama_alt", Hashirama, 6, Warm, 0, false}, {"madara", Madara, 4, Warm, 0, false},
		{"obito", Obito, 4, Purple, 0, false}, {"naruto", Naruto, 4, Red, 0, false}, {"naruto_right", Naruto, 4, Red, 0, false},
		{"obito_current", Obito, 4, Purple, 0, false},
		{"sasuke_xiayin", SasukeXiayin, 4, Xiayin, 9, false},
		{"sasuke_xiayin_duel", SasukeXiayin, 4, Xiayin, 9, false},
		// User-provided native captures. These full-title templates establish
		// a display identity; they retain the ordinary four-slot/default-color
		// policy. Minato is the reviewed energy-gauge row-offset exception;
		// four slots let the tracker keep that row through brief title gaps.
		{"itachi_hyakusen", ItachiHyakusen, 4, "", EnergyGaugeRowOffset, true},
		// MuMu 1280x720 capture requires independent character HUD portrait evidence.
		{"itachi_hyakusen_mumu", ItachiHyakusen, 4, "", EnergyGaugeRowOffset, true},
		{"minato_kyubi", MinatoKyubi, 4, "", EnergyGaugeRowOffset, false},
		{"hashirama_edo", HashiramaEdo, 0, "", 0, false},
		{"naruto_student", NarutoStudent, 0, "", 0, false},
	} {
		data, err := templates.ReadFile("templates/" + spec.file + ".png")
		if err != nil {
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			continue
		}
		portrait := (*image.Gray)(nil)
		if spec.name == ItachiHyakusen {
			if portraitData, portraitErr := templates.ReadFile("templates/itachi_hyakusen_portrait.png"); portraitErr == nil {
				if portraitImage, _, decodeErr := image.Decode(bytes.NewReader(portraitData)); decodeErr == nil {
					portrait = match.ToGray(portraitImage)
				}
			}
		}
		r.source = append(r.source, nameTemplate{readout: Readout{Name: spec.name, Slots: spec.slots, Palette: spec.palette, RowOffsetY: spec.rowOffsetY}, gray: match.ToGray(img), portrait: portrait, requirePortrait: spec.requirePortrait})
	}
	return r
}

// AvatarEnabled reports whether a validated external catalog is available.
func (r *Reader) AvatarEnabled() bool { return r != nil && r.avatars != nil }

// Read uses scale relative to the supplied 960-wide HUD reference (15px
// nominal slot pitch), never the arbitrary width of a tightly cropped image.
// The cache holds a few recent scales and owns immutable templates.
func (r *Reader) Read(img *image.RGBA, roi image.Rectangle, scale float64) Readout {
	return r.read(img, roi, scale).Readout
}

type evidence struct {
	Readout
	template     *match.PreparedNCC
	rect         image.Rectangle
	portrait     *match.PreparedNCC
	portraitSize image.Point
}

// portraitAnchor places 百战鼬's fixed right-HUD portrait check on a
// calibrated right side: relative to its avatar window instead of the 16:9
// frame origin. The zero value keeps the built-in frame-origin rect.
type portraitAnchor struct {
	window image.Rectangle
	scale  float64
}

func (r *Reader) read(img *image.RGBA, roi image.Rectangle, scale float64) evidence {
	return r.readAnchored(img, roi, scale, portraitAnchor{})
}

func (r *Reader) readAnchored(img *image.RGBA, roi image.Rectangle, scale float64, anchor portraitAnchor) evidence {
	if r == nil || img == nil || scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return evidence{}
	}
	r.mu.Lock()
	prepared := r.preparedAt(scale)
	r.mu.Unlock()
	// 百战鼬 requires two independent, current-frame signals: its complete title
	// and its fixed right-HUD portrait. The portrait alone cannot move the bean
	// row or turn a base Itachi into the 百战 skin.
	roi = roi.Intersect(img.Bounds())
	if roi.Empty() {
		return evidence{}
	}
	view := img.SubImage(roi).(*image.RGBA)
	gray := match.ToGray(view)
	// Unknown names used to scan the whole corner at native resolution. Keep
	// the locator bounded at the 480-wide HUD scale, then validate candidate
	// locations using ALL original-resolution pixels and the original threshold.
	factor := min(scale, .5) / scale
	smallGray := match.ScaleGray(gray, max(1, int(math.Round(float64(gray.Bounds().Dx())*factor))), max(1, int(math.Round(float64(gray.Bounds().Dy())*factor))))
	// NCC reads only the Image bounds when Gray is supplied.
	smallImage := &image.RGBA{Rect: smallGray.Bounds()}
	// Each template's locate+refine pair is independent and only reads the
	// shared pixels and its immutable prepared templates, so they run
	// concurrently. The reduction below stays sequential in prepared order with
	// the same strict '>', so ties still go to the first template.
	type titleResult struct {
		score    match.Score
		template fineTemplate // the size that matched
		ok       bool
	}
	results := make([]titleResult, len(prepared))
	parallelFor(len(prepared), func(i int) {
		t := prepared[i]
		coarse, err := (match.NCC{}).Match(match.Query{Image: smallImage, Gray: smallGray, ROI: smallImage.Bounds(), Prepared: t.coarse})
		if err != nil || coarse.Value < .55 {
			return
		}
		point := image.Pt(roi.Min.X+int(math.Round(float64(coarse.Peak.X)/factor)), roi.Min.Y+int(math.Round(float64(coarse.Peak.Y)/factor)))
		radius := int(math.Ceil(2 / factor))
		// Every size is scored at the same top-left positions around the
		// coarse peak; only its window grows to fit the size.
		fine := func(f fineTemplate) (match.Score, error) {
			candidate := image.Rectangle{Min: point, Max: point.Add(f.size)}.Inset(-radius).Intersect(roi)
			return (match.NCC{}).Match(match.Query{Image: view, Gray: gray, ROI: candidate, Prepared: f.ncc})
		}
		nominal := fineTemplate{ncc: t.ncc, size: t.size}
		score, err := fine(nominal)
		if err != nil {
			return
		}
		result := titleResult{score, nominal, true}
		for _, v := range t.variants.get() {
			if score, err := fine(v); err == nil && score.Value > result.score.Value {
				result = titleResult{score, v, true}
			}
		}
		results[i] = result
	})
	var best evidence
	scores := map[string]float64{}
	for i, t := range prepared {
		if !results[i].ok {
			continue
		}
		score := results[i].score
		scores[t.readout.Name] = max(scores[t.readout.Name], score.Value)
		if score.Value > best.Score {
			// The Tracker rechecks exactly the size that matched.
			matched := results[i].template
			best = evidence{Readout: t.readout, template: matched.ncc, rect: image.Rectangle{Min: score.Peak, Max: score.Peak.Add(matched.size)}}
			best.Score = score.Value
		}
	}
	runnerUp := 0.0
	for name, score := range scores {
		if name != best.Name {
			runnerUp = max(runnerUp, score)
		}
	}
	if best.Score < 0.80 || best.Score-runnerUp < 0.08 {
		return evidence{}
	}
	// 百战鼬 requires two independent, current-frame signals: its complete title
	// and its fixed right-HUD portrait. Keep the verified portrait template on
	// the evidence so the Tracker's fast path can re-check it on later frames.
	if best.Name == ItachiHyakusen {
		t := scaledNameForEvidence(prepared, best)
		if !itachiPortraitEvidenceAt(img, scale, t.portrait, t.portraitSize, anchor) {
			return evidence{}
		}
		best.portrait, best.portraitSize = t.portrait, t.portraitSize
	}
	return best
}

// preparedAt returns the templates prepared for scale, building them on first
// use (caller holds mu). Prepared sets depend only on the scale, so a cached
// set is exactly what rebuilding would produce.
func (r *Reader) preparedAt(scale float64) []scaledName {
	for i, s := range r.scales {
		if s.scale == scale {
			copy(r.scales[1:i+1], r.scales[:i])
			r.scales[0] = s
			return s.prepared
		}
	}
	var prepared []scaledName
	for _, t := range r.source {
		w, h := int(math.Round(float64(t.gray.Bounds().Dx())*scale)), int(math.Round(float64(t.gray.Bounds().Dy())*scale))
		if w < 8 || h < 8 {
			continue
		}
		gray := match.ScaleGray(t.gray, w, h)
		coarseScale := min(scale, .5)
		small := match.ScaleGray(t.gray, int(math.Round(float64(t.gray.Bounds().Dx())*coarseScale)), int(math.Round(float64(t.gray.Bounds().Dy())*coarseScale)))
		p := scaledName{readout: t.readout, ncc: match.PrepareNCC(gray, nil), size: gray.Bounds().Size(), coarse: match.PrepareNCC(small, nil), requirePortrait: t.requirePortrait}
		p.variants = &fineVariants{src: t.gray, scale: scale, nominal: p.size}
		if t.portrait != nil {
			pw := int(math.Round(float64(t.portrait.Bounds().Dx()) * scale))
			ph := int(math.Round(float64(t.portrait.Bounds().Dy()) * scale))
			portrait := match.ScaleGray(t.portrait, pw, ph)
			p.portrait = match.PrepareNCC(portrait, nil)
			p.portraitSize = portrait.Bounds().Size()
		}
		prepared = append(prepared, p)
	}
	if len(r.scales) < readerScales {
		r.scales = append(r.scales, readerScale{})
	}
	copy(r.scales[1:], r.scales[:len(r.scales)-1])
	r.scales[0] = readerScale{scale: scale, prepared: prepared}
	return prepared
}

// ResolveEvidence decides identity from two independent CURRENT-frame sources.
// Exact complete titles win when present; an avatar can independently confirm a
// title or produce a bounded candidate only if it identifies an exact catalog
// variant. A base title never turns an avatar into a special variant.
func (r *Reader) ResolveEvidence(img *image.RGBA, titleROI image.Rectangle, avatarROI image.Rectangle, scale float64, title Readout, avatar *AvatarTracker, now time.Time) Readout {
	out := title
	out.TitleName = title.Name
	if r == nil || r.avatars == nil || avatarROI.Empty() {
		return out
	}
	m := avatar.Read(r.avatars, img, avatarROI, scale, now)
	out.AvatarName, out.AvatarScore = m.Name, m.Score
	if title.Name != "" {
		if m.Name != "" && !sameAvatarEvidence(title.Name, m) {
			// A disagreement never enables a version-dependent geometry rule.
			return Readout{TitleName: title.Name, AvatarName: m.Name, AvatarScore: m.Score}
		}
		// Current complete title is still independently sufficient. Portrait
		// absence (crop/skin mismatch) does not regress reviewed title paths.
		out.Name = title.Name
		return out
	}
	if m.Name != "" {
		// A strong, separated current avatar can supply a bounded exact variant
		// when a long account name covers the title. Only explicit variant
		// policies below alter slots/palette/row geometry; other catalog entries
		// remain display identity only.
		return avatarReadout(m)
	}
	return out
}

func scaledNameForEvidence(prepared []scaledName, found evidence) scaledName {
	for _, t := range prepared {
		if t.readout.Name == found.Name && t.requirePortrait {
			return t
		}
	}
	return scaledName{}
}

func sameAvatarEvidence(title string, m AvatarMatch) bool {
	if sameAvatarVariant(title, m.Name) {
		return true
	}
	// The HUD keeps the base form in the title while an equipped seasonal skin
	// changes only the portrait (骥玄凌霄 over 宇智波斑[神驹佑将]). The title's
	// base role may therefore corroborate that skin, but the full title keeps
	// authority: this must never turn the skin into the base variant or enable
	// a skin-only geometry rule.
	return m.BaseName != "" && normalizeAvatarName(avatarTitleRole(title)) == normalizeAvatarName(m.BaseName)
}

// avatarTitleRole extracts the ninja role before the first variant bracket.
func avatarTitleRole(title string) string {
	if i := strings.IndexAny(title, "[【「("); i >= 0 {
		return title[:i]
	}
	return title
}

func avatarReadout(m AvatarMatch) Readout {
	name := canonicalAvatarName(m.Name)
	out := Readout{Name: name, AvatarName: name, AvatarScore: m.Score}
	switch normalizeAvatarName(name) {
	case normalizeAvatarName(Obito):
		out.Slots, out.Palette = 4, Purple
	case normalizeAvatarName(SasukeXiayin):
		out.Slots, out.Palette, out.RowOffsetY = 4, Xiayin, 9
	case normalizeAvatarName(ItachiHyakusen):
		// Corpus ID 920541 is 宇智波鼬[百战]. Only that exact current avatar
		// variant gets the ordinary four-slot row and its 13px energy shift.
		out.Slots, out.RowOffsetY = 4, EnergyGaugeRowOffset
	case normalizeAvatarName(MinatoKyubi):
		// Catalog ID 90316 is exactly 波风水门[九喇嘛连结]: same energy row.
		out.Slots, out.RowOffsetY = 4, EnergyGaugeRowOffset
	}
	return out
}

func itachiPortraitEvidence(img *image.RGBA, scale float64, portrait *match.PreparedNCC, size image.Point) bool {
	return itachiPortraitEvidenceAt(img, scale, portrait, size, portraitAnchor{})
}

func itachiPortraitEvidenceAt(img *image.RGBA, scale float64, portrait *match.PreparedNCC, size image.Point, anchor portraitAnchor) bool {
	if portrait == nil || size.X <= 0 || size.Y <= 0 {
		return false
	}
	bounds := img.Bounds()
	if anchor.scale > 0 {
		// The fixed rect below is (8, 9) units inside the camp right window;
		// a hand-drawn calibration may be off by up to 2 units.
		at := anchor.window.Min.Add(image.Pt(int(math.Round(8*anchor.scale)), int(math.Round(9*anchor.scale))))
		rect := image.Rectangle{Min: at, Max: at.Add(size)}.Inset(-max(1, int(math.Round(2*anchor.scale)))).Intersect(bounds)
		if rect.Dx() < size.X || rect.Dy() < size.Y {
			return false
		}
		score, ok := itachiPortraitScore(img, rect, portrait)
		return ok && score >= .78
	}
	// The supplied portrait is a right-HUD visual anchor. Do not mirror it to
	// the left: insufficient evidence must remain unknown, never a guessed
	// special offset. Coordinates are normalized to the 960-wide HUD reference.
	x0, x1, y0 := 847.5, 918.75, 7.5
	rect := image.Rect(bounds.Min.X+int(math.Round(x0*scale)), bounds.Min.Y+int(math.Round(y0*scale)), bounds.Min.X+int(math.Round(x1*scale)), bounds.Min.Y+int(math.Round((y0+71)*scale))).Intersect(bounds)
	if rect.Size() != size {
		return false
	}
	score, ok := itachiPortraitScore(img, rect, portrait)
	return ok && score >= .78
}

// itachiPortraitScore converts only the portrait rect to gray. NCC offsets
// are relative to the view's bounds and its peak stays in frame coordinates,
// so the score equals matching the full-frame gray over the same rect.
func itachiPortraitScore(img *image.RGBA, rect image.Rectangle, portrait *match.PreparedNCC) (float64, bool) {
	view := img.SubImage(rect).(*image.RGBA)
	score, err := (match.NCC{}).Match(match.Query{Image: view, Gray: match.ToGray(view), ROI: rect, Prepared: portrait})
	return score.Value, err == nil
}

// NameRegion is deliberately above the calibrated bean row. A small extra
// upper band keeps the search stable for compact duel HUDs whose title baseline
// sits above the normal blood-bar offset; it does not move any bean center or
// authorize a slot/palette rule. Blood bars and full-screen effects do not get
// to move the sampling centers.
func NameRegion(first image.Point, scale float64, left bool) image.Rectangle {
	x0, x1 := -6.0, 340.0
	if !left {
		// MuMu 1280x720 right titles can extend a few pixels beyond the
		// right-side first-bead anchor; keep the bounded ROI symmetric enough
		// to include the complete current title without searching the HUD.
		x0, x1 = -340.0, 16.0
	}
	return image.Rect(first.X+int(math.Round(x0*scale)), first.Y-int(math.Round(64*scale)), first.X+int(math.Round(x1*scale)), first.Y-int(math.Round(13*scale)))
}
