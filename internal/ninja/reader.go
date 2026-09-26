package ninja

import (
	"bytes"
	"embed"
	"image"
	_ "image/png"
	"math"
	"strings"
	"sync"
	"time"

	"narutotimer/internal/match"
)

// Name strips come from user-supplied HUD crops. Special strips establish the
// exact variant; ordinary strips establish only a display name (Slots == 0).
// Neither kind establishes the scene or the player's side.
//
//go:embed templates/*.png
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
	ncc             *match.PreparedNCC
	size            image.Point
	coarse          *match.PreparedNCC
	portrait        *match.PreparedNCC
	portraitSize    image.Point
	requirePortrait bool
}
type Reader struct {
	mu       sync.Mutex
	source   []nameTemplate
	scale    float64
	prepared []scaledName
	avatars  *avatarCatalog
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
		// policy. Minato is the reviewed energy-gauge row-offset exception.
		{"itachi_hyakusen", ItachiHyakusen, 4, "", EnergyGaugeRowOffset, true},
		// MuMu 1280x720 capture requires independent character HUD portrait evidence.
		{"itachi_hyakusen_mumu", ItachiHyakusen, 4, "", EnergyGaugeRowOffset, true},
		{"minato_kyubi", MinatoKyubi, 0, "", EnergyGaugeRowOffset, false},
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
// The cache holds only one scale and owns immutable templates.
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

func (r *Reader) read(img *image.RGBA, roi image.Rectangle, scale float64) evidence {
	if r == nil || img == nil || scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return evidence{}
	}
	r.mu.Lock()
	if r.prepared == nil || r.scale != scale {
		r.prepared = nil
		r.scale = scale
		for _, t := range r.source {
			w, h := int(math.Round(float64(t.gray.Bounds().Dx())*scale)), int(math.Round(float64(t.gray.Bounds().Dy())*scale))
			if w < 8 || h < 8 {
				continue
			}
			gray := match.ScaleGray(t.gray, w, h)
			coarseScale := min(scale, .5)
			small := match.ScaleGray(t.gray, int(math.Round(float64(t.gray.Bounds().Dx())*coarseScale)), int(math.Round(float64(t.gray.Bounds().Dy())*coarseScale)))
			prepared := scaledName{readout: t.readout, ncc: match.PrepareNCC(gray, nil), size: gray.Bounds().Size(), coarse: match.PrepareNCC(small, nil), requirePortrait: t.requirePortrait}
			if t.portrait != nil {
				pw := int(math.Round(float64(t.portrait.Bounds().Dx()) * scale))
				ph := int(math.Round(float64(t.portrait.Bounds().Dy()) * scale))
				portrait := match.ScaleGray(t.portrait, pw, ph)
				prepared.portrait = match.PrepareNCC(portrait, nil)
				prepared.portraitSize = portrait.Bounds().Size()
			}
			r.prepared = append(r.prepared, prepared)
		}
	}
	prepared := r.prepared
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
	smallImage := image.NewRGBA(smallGray.Bounds())
	var best evidence
	scores := map[string]float64{}
	for _, t := range prepared {
		coarse, err := (match.NCC{}).Match(match.Query{Image: smallImage, Gray: smallGray, ROI: smallImage.Bounds(), Prepared: t.coarse})
		if err != nil || coarse.Value < .55 {
			continue
		}
		point := image.Pt(roi.Min.X+int(math.Round(float64(coarse.Peak.X)/factor)), roi.Min.Y+int(math.Round(float64(coarse.Peak.Y)/factor)))
		radius := int(math.Ceil(2 / factor))
		candidate := image.Rectangle{Min: point, Max: point.Add(t.size)}.Inset(-radius).Intersect(roi)
		score, err := (match.NCC{}).Match(match.Query{Image: view, Gray: gray, ROI: candidate, Prepared: t.ncc})
		if err != nil {
			continue
		}
		scores[t.readout.Name] = max(scores[t.readout.Name], score.Value)
		if score.Value > best.Score {
			best = evidence{Readout: t.readout, template: t.ncc, rect: image.Rectangle{Min: score.Peak, Max: score.Peak.Add(t.size)}}
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
		if !itachiPortraitEvidence(img, scale, t.portrait, t.portraitSize) {
			return evidence{}
		}
		best.portrait, best.portraitSize = t.portrait, t.portraitSize
	}
	return best
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
	}
	return out
}

func itachiPortraitEvidence(img *image.RGBA, scale float64, portrait *match.PreparedNCC, size image.Point) bool {
	if portrait == nil || size.X <= 0 || size.Y <= 0 {
		return false
	}
	bounds := img.Bounds()
	// The supplied portrait is a right-HUD visual anchor. Do not mirror it to
	// the left: insufficient evidence must remain unknown, never a guessed
	// special offset. Coordinates are normalized to the 960-wide HUD reference.
	x0, x1, y0 := 847.5, 918.75, 7.5
	rect := image.Rect(bounds.Min.X+int(math.Round(x0*scale)), bounds.Min.Y+int(math.Round(y0*scale)), bounds.Min.X+int(math.Round(x1*scale)), bounds.Min.Y+int(math.Round((y0+71)*scale))).Intersect(bounds)
	if rect.Size() != size {
		return false
	}
	gray := match.ToGray(img)
	score, err := (match.NCC{}).Match(match.Query{Image: img, Gray: gray, ROI: rect, Prepared: portrait})
	return err == nil && score.Value >= .78
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
