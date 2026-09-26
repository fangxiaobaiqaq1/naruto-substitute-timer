package ninja

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"narutotimer/assets"
	"narutotimer/internal/match"
)

// AvatarOptions supports an optional external catalog for development. The
// Windows release uses the embedded, index-verified A/S catalog by default.
type AvatarOptions struct {
	Directory string
	Index     string
}

type avatarIndex struct {
	Entries []avatarIndexEntry `json:"entries"`
}

// AvatarCatalogStats is useful in diagnostics/tests without exposing asset
// storage or allowing callers to assume directory ordering.
type AvatarCatalogStats struct {
	Entries int
	Base    int
	Skins   int
	Bytes   int
}

type avatarIndexEntry struct {
	ID            string `json:"id"`
	IsSkin        bool   `json:"isSkin"`
	Ninja         string `json:"ninja"`
	Form          string `json:"form"`
	SkinTitle     string `json:"skinTitle"`
	BaseNinjaID   string `json:"baseNinjaId"`
	BaseNinjaName string `json:"baseNinjaName"`
	CanonicalName string `json:"canonicalName"`
	Avatar        struct {
		SHA256 string `json:"sha256"`
	} `json:"avatar"`
	SHA256 string `json:"sha256"` // bundled manifest format
}

type avatarEntry struct {
	id, name, baseName string
	data               []byte
	thumb              *match.PreparedNCC
	thumbGray          *image.Gray
	thumbMask          *image.Gray
	coarseGray         *image.Gray
	coarseMask         *image.Gray
	gray               *image.Gray
	mask               *image.Gray
	scaled             map[string]*match.PreparedNCC
}

// AvatarMatch is exclusively current-frame portrait evidence. Candidate is
// diagnostic-only when the strict identity fields are blank.
type AvatarMatch struct {
	ID string
	// Name is the canonical identity for this exact portrait asset.
	Name string
	// BaseName is the official base ninja identity, retained independently of
	// the skin-specific Name. It is only used to corroborate a current full
	// title; avatar-only recognition never downgrades a skin to its base form.
	BaseName  string
	Score     float64
	RunnerUp  float64
	Candidate string
	ids       []string
}

type avatarCatalog struct {
	mu            sync.Mutex
	entries       []*avatarEntry
	byID          map[string]*avatarEntry
	cacheScale    float64
	cachedEntries int
}

// AvatarTracker executes the 242-entry thumbnail filter at most twice per
// second per side. Other frames re-score only the previous six candidates on
// current pixels; no identity is inherited from a prior frame.
type AvatarTracker struct {
	next        time.Time
	ids         []string
	catalog     *avatarCatalog
	bounds, roi image.Rectangle
	scale       float64
	lastAt      time.Time
}

var embeddedAvatarCatalog = sync.OnceValues(func() (*avatarCatalog, error) {
	return loadAvatarCatalog(assets.ASAvatarIndex, func(id string) ([]byte, error) {
		return assets.ASAvatars.ReadFile("avatars/" + id + ".png")
	})
})

func loadEmbeddedAvatarCatalog() (*avatarCatalog, error) { return embeddedAvatarCatalog() }

func AvatarStats() (AvatarCatalogStats, error) {
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		return AvatarCatalogStats{}, err
	}
	stats := AvatarCatalogStats{Entries: len(c.entries)}
	for _, e := range c.entries {
		stats.Bytes += len(e.data)
		if len(e.id) == 5 {
			stats.Base++
		} else {
			stats.Skins++
		}
	}
	return stats, nil
}

// LoadAvatarCatalog validates an external official-index catalog. It exists for
// reproducible imports and tests; a bad optional directory never replaces the
// verified embedded catalog in NewReader.
func LoadAvatarCatalog(options AvatarOptions) (*avatarCatalog, error) {
	if options.Directory == "" && options.Index == "" {
		return nil, nil
	}
	if options.Directory == "" || options.Index == "" {
		return nil, fmt.Errorf("avatar catalog requires both directory and index")
	}
	data, err := os.ReadFile(options.Index)
	if err != nil {
		return nil, fmt.Errorf("read avatar index: %w", err)
	}
	return loadAvatarCatalog(data, func(id string) ([]byte, error) { return os.ReadFile(filepath.Join(options.Directory, id+".png")) })
}

func loadAvatarCatalog(data []byte, readFile func(string) ([]byte, error)) (*avatarCatalog, error) {
	var index avatarIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parse avatar index: %w", err)
	}
	if len(index.Entries) == 0 {
		return nil, fmt.Errorf("avatar index has no entries")
	}
	catalog := &avatarCatalog{entries: make([]*avatarEntry, 0, len(index.Entries)), byID: make(map[string]*avatarEntry, len(index.Entries))}
	for _, item := range index.Entries {
		sum := item.SHA256
		if sum == "" {
			sum = item.Avatar.SHA256
		}
		if !validAvatarID(item.ID) || catalog.byID[item.ID] != nil || len(sum) != 64 {
			return nil, fmt.Errorf("invalid or duplicate avatar id %q", item.ID)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("invalid avatar sha256 for %s", item.ID)
		}
		png, err := readFile(item.ID)
		if err != nil {
			return nil, fmt.Errorf("read avatar %s: %w", item.ID, err)
		}
		got := sha256.Sum256(png)
		if !strings.EqualFold(hex.EncodeToString(got[:]), sum) {
			return nil, fmt.Errorf("avatar %s sha256 does not match index", item.ID)
		}
		img, _, err := image.Decode(bytes.NewReader(png))
		if err != nil {
			return nil, fmt.Errorf("decode avatar %s: %w", item.ID, err)
		}
		gray, alphaMask := avatarGrayMask(img)
		if gray.Bounds().Dx() < 16 || gray.Bounds().Dy() < 16 {
			return nil, fmt.Errorf("avatar %s too small", item.ID)
		}
		// The catalog PNG carries a baked rank badge/frame/background. Those
		// pixels vary in the live HUD, while the face inside the diamond is
		// stable. Recognition uses alpha ∩ inner-diamond only.
		mask := avatarFaceMask(gray.Bounds(), alphaMask)
		thumbGray, thumbMask := match.ScaleGray(gray, 16, 16), match.ScaleGray(mask, 16, 16)
		entry := &avatarEntry{id: item.ID, name: avatarName(item), baseName: avatarBaseName(item), data: png, thumb: match.PrepareNCC(thumbGray, thumbMask), thumbGray: thumbGray, thumbMask: thumbMask, coarseGray: match.ScaleGray(gray, 36, 36), coarseMask: match.ScaleGray(mask, 36, 36)}
		catalog.entries, catalog.byID[item.ID] = append(catalog.entries, entry), entry
	}
	return catalog, nil
}

func validAvatarID(id string) bool {
	if len(id) != 5 && len(id) != 6 {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func avatarBaseName(item avatarIndexEntry) string {
	base := item.Ninja
	if item.IsSkin {
		base = item.BaseNinjaName
	}
	if i := strings.IndexAny(base, "「["); i >= 0 {
		base = base[:i]
	}
	if base == "" {
		base = item.Ninja
	}
	return base
}

func avatarName(item avatarIndexEntry) string {
	base, form := item.Ninja, item.Form
	if item.IsSkin {
		base, form = item.BaseNinjaName, item.SkinTitle
	}
	if i := strings.IndexAny(base, "「["); i >= 0 {
		base = base[:i]
	}
	if base == "" {
		base = item.Ninja
	}
	if form == "" {
		return base
	}
	return base + "[" + form + "]"
}

// avatarFaceMask keeps alpha ∩ the inner diamond: the baked rank badge, frame,
// and background around the portrait vary in the live HUD, the face does not.
func avatarFaceMask(bounds image.Rectangle, alpha *image.Gray) *image.Gray {
	mask := image.NewGray(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	cx, cy := float64(bounds.Dx()-1)/2, float64(bounds.Dy()-1)/2
	radius := float64(min(bounds.Dx(), bounds.Dy())) * 0.43
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			if alpha.GrayAt(alpha.Bounds().Min.X+x, alpha.Bounds().Min.Y+y).Y >= 128 && math.Abs(float64(x)-cx)+math.Abs(float64(y)-cy) <= radius {
				mask.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	return mask
}

func avatarGrayMask(src image.Image) (*image.Gray, *image.Gray) {
	b := src.Bounds()
	gray, mask := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy())), image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			gray.SetGray(x, y, color.Gray{Y: uint8((299*(r>>8) + 587*(g>>8) + 114*(bl>>8) + 500) / 1000)})
			if a >= 0x8000 {
				mask.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	return gray, mask
}

func (c *avatarCatalog) Match(img *image.RGBA, roi image.Rectangle, scale float64) AvatarMatch {
	if c == nil || img == nil || scale <= 0 {
		return AvatarMatch{}
	}
	roi = roi.Intersect(img.Bounds())
	if roi.Empty() {
		return AvatarMatch{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	view := img.SubImage(roi).(*image.RGBA)
	coarse := match.ScaleGray(match.ToGray(view), 36, 36)
	// The portrait is a fixed HUD element: relative to the canonical
	// AvatarRegion window its diamond center sits near (35.5, 48) reference
	// pixels and renders at roughly 0.6x the 85px catalog asset. A square crop
	// around that anchor removes the ROI's background and vertical squash from
	// the 36x36 locator. The unanchored crop stays as a fallback for callers
	// that pass a synthetic centered portrait rather than the HUD ROI.
	hudCoarse := match.ScaleGray(match.ToGray(match.CropRGBA(view, avatarAnchorRect(roi, scale))), 36, 36)
	type finalist struct {
		entry *avatarEntry
		score float64
	}
	best := make([]finalist, 0, len(c.entries))
	for _, entry := range c.entries {
		// Two aligned 36² thumbnail queries (HUD anchor + whole ROI) keep
		// recall for both real HUD frames and centered synthetic portraits.
		// Only the retained candidates do full-resolution NCC.
		score := coarseAvatarScore(coarse, entry.coarseGray, entry.coarseMask)
		if hud := coarseAvatarScore(hudCoarse, entry.coarseGray, entry.coarseMask); hud > score {
			score = hud
		}
		best = append(best, finalist{entry, score})
	}
	sort.Slice(best, func(i, j int) bool { return best[i].score > best[j].score })
	// Coarse matching is only a locator. A real HUD portrait can be partially
	// clipped by the capture boundary and its render scale is not guaranteed to
	// equal the bead-derived scale, so the true entry may rank below the first
	// few color-similar portraits (measured worst case: 水门 at rank 10). Keep
	// this bounded recall set; strict multi-scale NCC and the score margin
	// still decide identity.
	if len(best) > 24 {
		best = best[:24]
	}
	entries := make([]*avatarEntry, len(best))
	for i := range best {
		entries[i] = best[i].entry
	}
	return c.matchEntries(view, scale, entries)
}

// avatarAnchorRect maps the measured HUD diamond neighborhood into img pixels.
// Coordinates are relative to the AvatarRegion window and normalized to the
// 960-wide HUD reference.
func avatarAnchorRect(roi image.Rectangle, scale float64) image.Rectangle {
	const cx, cy, half = 35.5, 48.0, 34.0
	return image.Rect(
		roi.Min.X+int(math.Round((cx-half)*scale)),
		roi.Min.Y+int(math.Round((cy-half)*scale)),
		roi.Min.X+int(math.Round((cx+half)*scale)),
		roi.Min.Y+int(math.Round((cy+half)*scale)),
	)
}

func coarseAvatarScore(query, templ, mask *image.Gray) float64 {
	if query == nil || templ == nil || mask == nil || query.Bounds().Size() != templ.Bounds().Size() || templ.Bounds().Size() != mask.Bounds().Size() {
		return 0
	}
	var difference, count int
	for y := range templ.Bounds().Dy() {
		for x := range templ.Bounds().Dx() {
			i := y*templ.Stride + x
			if mask.Pix[y*mask.Stride+x] < 128 {
				continue
			}
			d := int(query.Pix[y*query.Stride+x]) - int(templ.Pix[i])
			if d < 0 {
				d = -d
			}
			difference += d
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return 1 - float64(difference)/float64(255*count)
}

// avatarFactorsFull is the strict multi-scale display-factor sweep. The real
// HUD renders the 85px catalog diamond near 0.6x the bead-derived scale; the
// larger factors only serve exact-scale callers (tests, legacy captures).
var avatarFactorsFull = []float64{.55, .60, .65, .70, .75, .85, 1.0, 1.15, 1.30, 1.45}

func (c *avatarCatalog) matchEntries(view *image.RGBA, scale float64, entries []*avatarEntry) AvatarMatch {
	var out AvatarMatch
	// Keep the best score per identity before calculating the runner-up. A
	// single identity is evaluated at several display scales; treating its
	// second scale as a different candidate can reject an otherwise unambiguous
	// portrait (best=.90, same-ID runner-up=.85).
	bestByID := make(map[string]float64, len(entries))
	nameByID := make(map[string]string, len(entries))
	baseByID := make(map[string]string, len(entries))
	bounds := view.Bounds()
	c.evictTemplates(scale)
	decode := func(entry *avatarEntry) bool {
		if entry == nil {
			return false
		}
		if entry.gray == nil {
			img, _, err := image.Decode(bytes.NewReader(entry.data))
			if err != nil {
				return false
			}
			entry.gray, entry.mask = avatarGrayMask(img)
			entry.mask = avatarFaceMask(entry.gray.Bounds(), entry.mask)
		}
		return true
	}
	scoreAt := func(entry *avatarEntry, factor float64) float64 {
		w, h := avatarFactorSize(entry, scale, factor)
		if w > bounds.Dx() || h > bounds.Dy() {
			// NCC cannot slide a template larger than the ROI; skip the wasted
			// prepare instead of measuring a meaningless zero score.
			return 0
		}
		score, err := (match.NCC{}).Match(match.Query{Image: view, ROI: view.Bounds(), Prepared: c.prepared(entry, w, h)})
		if err != nil {
			return 0
		}
		return score.Value
	}
	for _, entry := range entries {
		if !decode(entry) {
			continue
		}
		out.ids = append(out.ids, entry.id)
		nameByID[entry.id] = entry.name
		baseByID[entry.id] = entry.baseName
	}
	if len(entries) > 6 {
		// Full scan, two-stage: the primary display factor (the live HUD
		// renders near 0.6x bead scale) locates the portrait cheaply over the
		// whole shortlist; the full multi-scale sweep below then re-runs only
		// the leading candidates plus the coarse locator's first entries. The
		// shortlist order itself is the recall fallback for render scales that
		// are not the common HUD factor (exact-scale test callers).
		for _, entry := range entries {
			if value := scoreAt(entry, .60); value > bestByID[entry.id] {
				bestByID[entry.id] = value
			}
		}
		primary := make([]stageEntry, 0, len(entries))
		for _, entry := range entries {
			if entry != nil {
				primary = append(primary, stageEntry{entry, bestByID[entry.id]})
			}
		}
		sort.Slice(primary, func(i, j int) bool { return primary[i].score > primary[j].score })
		fine := make(map[string]bool, 12)
		for i, v := range primary {
			if i < 6 {
				fine[v.entry.id] = true
			}
		}
		for i, entry := range entries {
			if entry != nil && i < 6 {
				fine[entry.id] = true
			}
		}
		for _, entry := range entries {
			if entry != nil && fine[entry.id] {
				for _, factor := range avatarFactorsFull {
					if value := scoreAt(entry, factor); value > bestByID[entry.id] {
						bestByID[entry.id] = value
					}
				}
			}
		}
	} else {
		for _, entry := range entries {
			for _, factor := range avatarFactorsFull {
				if value := scoreAt(entry, factor); value > bestByID[entry.id] {
					bestByID[entry.id] = value
				}
			}
		}
	}
	var runnerName string
	for id, score := range bestByID {
		if score > out.Score {
			out.RunnerUp, runnerName = out.Score, out.Name
			out.ID, out.Name, out.BaseName, out.Score = id, nameByID[id], baseByID[id], score
		} else if id != out.ID && score > out.RunnerUp {
			out.RunnerUp, runnerName = score, nameByID[id]
		}
	}
	out.Candidate = out.Name
	if out.Score < .78 || out.Score-out.RunnerUp < .08 {
		// A lookalike skin pair of the same cosmetic family (both golden Naruto
		// forms under a glare) can compress the margin below the strict gate.
		// Accept a very strong winner only when NEITHER candidate carries a
		// special variant policy: geometry rules (slots/palette/row offset)
		// stay strictly margined, so special-bead safety is unchanged.
		if !(out.Score >= .90 && out.Score-out.RunnerUp >= .02 && !avatarVariantPolicy(out.Name) && !avatarVariantPolicy(runnerName)) {
			out.ID, out.Name, out.BaseName = "", "", ""
		}
	}
	return out
}

type stageEntry struct {
	entry *avatarEntry
	score float64
}

// avatarVariantPolicy reports whether this catalog display name carries a
// special slot/palette/geometry policy. Such identities must never be decided
// by the relaxed same-role margin path.
func avatarVariantPolicy(name string) bool {
	switch normalizeAvatarName(canonicalAvatarName(name)) {
	case normalizeAvatarName(Obito), normalizeAvatarName(SasukeXiayin), normalizeAvatarName(ItachiHyakusen):
		return true
	}
	return false
}

func avatarFactorSize(entry *avatarEntry, scale, factor float64) (int, int) {
	return max(12, int(math.Round(float64(entry.gray.Bounds().Dx())*scale*factor))), max(12, int(math.Round(float64(entry.gray.Bounds().Dy())*scale*factor)))
}

// evictTemplates clears the per-entry scaled template cache whenever the HUD
// scale changes; cached templates are only valid for one capture geometry.
func (c *avatarCatalog) evictTemplates(scale float64) {
	if c.cacheScale != scale {
		c.cacheScale = scale
		for _, e := range c.entries {
			e.scaled = nil
		}
		c.cachedEntries = 0
	}
}

// prepared returns the cached scaled template for the entry at this size,
// preparing it on first use. All catalog template state lives under the
// catalog lock; the cache only ever holds templates at the current scale.
func (c *avatarCatalog) prepared(entry *avatarEntry, w, h int) *match.PreparedNCC {
	key := strconv.Itoa(w) + "x" + strconv.Itoa(h)
	if entry.scaled == nil {
		entry.scaled = make(map[string]*match.PreparedNCC, 8)
	} else if p, ok := entry.scaled[key]; ok {
		return p
	}
	p := match.PrepareNCC(match.ScaleGray(entry.gray, w, h), match.ScaleGray(entry.mask, w, h))
	entry.scaled[key] = p
	c.cachedEntries++
	if c.cachedEntries > 64 {
		for _, e := range c.entries {
			e.scaled = nil
		}
		c.cachedEntries = 0
	}
	return p
}

func (t *AvatarTracker) Read(c *avatarCatalog, img *image.RGBA, roi image.Rectangle, scale float64, now time.Time) AvatarMatch {
	if c == nil || img == nil || scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return AvatarMatch{}
	}
	roi = roi.Intersect(img.Bounds())
	if roi.Empty() {
		return AvatarMatch{}
	}
	// The candidate shortlist is only a geometry hint: any window, scale, or
	// catalog change (and backwards time) invalidates it. The very next frame
	// runs the bounded full scan again; identity itself is never inherited.
	changed := t.catalog != c || t.bounds != img.Bounds() || t.roi != roi || t.scale != scale || (!t.lastAt.IsZero() && now.Before(t.lastAt))
	t.lastAt = now
	if changed {
		t.ids, t.next = nil, time.Time{}
	}
	t.catalog, t.bounds, t.roi, t.scale = c, img.Bounds(), roi, scale
	if now.Before(t.next) && len(t.ids) > 0 {
		entries := make([]*avatarEntry, 0, len(t.ids))
		c.mu.Lock()
		for _, id := range t.ids {
			if entry := c.byID[id]; entry != nil {
				entries = append(entries, entry)
			}
		}
		out := c.matchEntries(img.SubImage(roi).(*image.RGBA), scale, entries)
		c.mu.Unlock()
		return out
	}
	out := c.Match(img, roi, scale)
	// The documented between-scan cost is the previous SIX candidates, never a
	// whole shortlist: an ambiguous full scan must not turn every intermediate
	// frame into a 24-entry NCC sweep.
	if len(out.ids) > 6 {
		out.ids = out.ids[:6]
	}
	t.ids = append(t.ids[:0], out.ids...)
	if out.ID != "" {
		t.ids = append(t.ids[:0], out.ID)
	}
	t.next = now.Add(500 * time.Millisecond)
	return out
}

// AvatarRegion maps one normalized 960-wide reference ROI from an existing
// first-bead anchor; it works for 720p..1440p and letterboxed content areas.
func AvatarRegion(first image.Point, scale float64, left bool) image.Rectangle {
	// The avatar asset is an 85x85 HUD diamond. Its bottom is below the bead
	// anchor by roughly 35 reference pixels; the previous -82..-5 window cut
	// off the lower half at the real 960-wide anchor (first.Y≈61), leaving a
	// badly warped top-only portrait. Keep the full diamond and allow the HUD
	// frame/health bar to be clipped by the image bounds at the call site.
	x0, x1 := -80.0, -10.0
	if !left {
		x0, x1 = 5, 75
	}
	return image.Rect(first.X+int(math.Round(x0*scale)), first.Y-int(math.Round(62*scale)), first.X+int(math.Round(x1*scale)), first.Y+int(math.Round(38*scale)))
}

// canonicalAvatarName maps only documented historical spellings onto a current
// canonical catalog title. It deliberately does not collapse distinct forms.
func canonicalAvatarName(s string) string {
	if normalizeAvatarName(s) == normalizeAvatarName(MadaraLegacyAlias) {
		return Madara
	}
	return s
}

func sameAvatarVariant(a, b string) bool {
	return normalizeAvatarName(canonicalAvatarName(a)) == normalizeAvatarName(canonicalAvatarName(b))
}

func normalizeAvatarName(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("[]【】「」()（）·・", r) {
			return -1
		}
		return r
	}, s)
}
