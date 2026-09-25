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
	"strings"
	"sync"
	"time"
	"unicode"

	"narutotimer/assets"
	"narutotimer/internal/detect"
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
	id, name   string
	data       []byte
	thumb      *match.PreparedNCC
	thumbGray  *image.Gray
	thumbMask  *image.Gray
	coarseGray *image.Gray
	coarseMask *image.Gray
	gray       *image.Gray
	mask       *image.Gray
}

// AvatarMatch is exclusively current-frame portrait evidence. Candidate is
// diagnostic-only when the strict identity fields are blank.
type AvatarMatch struct {
	ID        string
	Name      string
	Score     float64
	RunnerUp  float64
	Candidate string
	// Rect is the current-frame template footprint. It is empty unless the
	// match passed the normal score and separation requirements.
	Rect image.Rectangle
	// Scale/Profile/Side describe the current HUD search context. They make an
	// anchor usable by callers without allowing it to cross a layout or side.
	Scale   float64
	Profile string
	Side    string
	ids     []string
}

type avatarCatalog struct {
	mu      sync.Mutex
	entries []*avatarEntry
	byID    map[string]*avatarEntry
}

// AvatarTracker executes the 242-entry thumbnail filter at most twice per
// second per side. Other frames re-score only the previous six candidates on
// current pixels; no identity is inherited from a prior frame.
type AvatarTracker struct {
	next          time.Time
	ids           []string
	bounds, roi   image.Rectangle
	scale         float64
	profile, side string
	lastAt        time.Time
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
		gray, mask := avatarGrayMask(img)
		if gray.Bounds().Dx() < 16 || gray.Bounds().Dy() < 16 {
			return nil, fmt.Errorf("avatar %s too small", item.ID)
		}
		thumbGray, thumbMask := match.ScaleGray(gray, 16, 16), match.ScaleGray(mask, 16, 16)
		entry := &avatarEntry{id: item.ID, name: avatarName(item), data: png, thumb: match.PrepareNCC(thumbGray, thumbMask), thumbGray: thumbGray, thumbMask: thumbMask, coarseGray: match.ScaleGray(gray, 36, 36), coarseMask: match.ScaleGray(mask, 36, 36)}
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
	entries := c.coarseEntries([]image.Rectangle{roi}, img, 8)
	return c.matchEntries(view, scale, entries)
}

// coarseEntries uses normalized fixed-size portrait windows to cheaply retain
// a small set of catalog candidates. It deliberately never does a full-size
// catalog scan; full NCC below sees at most eight candidates.
func (c *avatarCatalog) coarseEntries(windows []image.Rectangle, img *image.RGBA, limit int) []*avatarEntry {
	type finalist struct {
		entry *avatarEntry
		score float64
	}
	// Convert each bounded current-frame window once. The catalog loop below
	// then only compares 36² bytes, rather than repeatedly converting pixels.
	queries := make([]*image.Gray, 0, len(windows))
	for _, window := range windows {
		window = window.Intersect(img.Bounds())
		if !window.Empty() {
			queries = append(queries, match.ScaleGray(match.ToGray(img.SubImage(window).(*image.RGBA)), 36, 36))
		}
	}
	best := make([]finalist, 0, len(c.entries))
	for _, entry := range c.entries {
		score := 0.0
		for _, coarse := range queries {
			score = max(score, coarseAvatarScore(coarse, entry.coarseGray, entry.coarseMask))
		}
		best = append(best, finalist{entry, score})
	}
	sort.Slice(best, func(i, j int) bool { return best[i].score > best[j].score })
	if len(best) > limit {
		best = best[:limit]
	}
	entries := make([]*avatarEntry, len(best))
	for i := range best {
		entries[i] = best[i].entry
	}
	return entries
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

func (c *avatarCatalog) matchEntries(view *image.RGBA, scale float64, entries []*avatarEntry) AvatarMatch {
	var out AvatarMatch
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		out.ids = append(out.ids, entry.id)
		if entry.gray == nil {
			img, _, err := image.Decode(bytes.NewReader(entry.data))
			if err != nil {
				continue
			}
			entry.gray, entry.mask = avatarGrayMask(img)
		}
		for _, factor := range []float64{.55, .70, .85, 1.0, 1.15, 1.30} {
			w, h := max(12, int(math.Round(float64(entry.gray.Bounds().Dx())*scale*factor))), max(12, int(math.Round(float64(entry.gray.Bounds().Dy())*scale*factor)))
			template := match.ScaleGray(entry.gray, w, h)
			score, err := (match.NCC{}).Match(match.Query{Image: view, ROI: view.Bounds(), Template: template, Mask: match.ScaleGray(entry.mask, w, h)})
			if err != nil {
				continue
			}
			if score.Value > out.Score {
				// NCC Peak is the matched template's upper-left screenshot pixel.
				// Use the actual scaled template footprint and bound it to this
				// current view; never carry a prior frame's anchor forward.
				rect := image.Rectangle{Min: score.Peak, Max: score.Peak.Add(template.Bounds().Size())}.Intersect(view.Bounds())
				out.RunnerUp, out.ID, out.Name, out.Score, out.Rect = out.Score, entry.id, entry.name, score.Value, rect
			} else if entry.id != out.ID && score.Value > out.RunnerUp {
				out.RunnerUp = score.Value
			}
		}
	}
	out.Candidate = out.Name
	if out.Score < .78 || out.Score-out.RunnerUp < .08 {
		out.ID, out.Name, out.Rect = "", "", image.Rectangle{}
	}
	return out
}

func (t *AvatarTracker) Read(c *avatarCatalog, img *image.RGBA, roi image.Rectangle, scale float64, now time.Time) AvatarMatch {
	return t.read(c, img, roi, scale, "", "", now, nil)
}

// Localize searches only the selected current HUD side. Its returned rectangle
// is a current-frame anchor, not tracker state: a blank or changed frame returns
// no identity and no anchor.
func (t *AvatarTracker) Localize(c *avatarCatalog, img *image.RGBA, area detect.ContentArea, profile string, left bool, scale float64, now time.Time) AvatarMatch {
	side := "right"
	if left {
		side = "left"
	}
	roi := AvatarSearchRegion(area, profile, left)
	return t.read(c, img, roi, scale, profile, side, now, func() AvatarMatch {
		return c.localize(img, roi, scale, profile, side)
	})
}

func (t *AvatarTracker) read(c *avatarCatalog, img *image.RGBA, roi image.Rectangle, scale float64, profile, side string, now time.Time, locate func() AvatarMatch) AvatarMatch {
	if c == nil || img == nil || scale <= 0 {
		return AvatarMatch{}
	}
	roi = roi.Intersect(img.Bounds())
	if roi.Empty() {
		return AvatarMatch{}
	}
	changed := t.bounds != img.Bounds() || t.roi != roi || t.scale != scale || t.profile != profile || t.side != side || (!t.lastAt.IsZero() && now.Before(t.lastAt))
	t.lastAt = now
	if changed {
		t.ids, t.next = nil, time.Time{}
	}
	t.bounds, t.roi, t.scale, t.profile, t.side = img.Bounds(), roi, scale, profile, side
	if now.Before(t.next) && len(t.ids) > 0 {
		entries := make([]*avatarEntry, 0, len(t.ids))
		c.mu.Lock()
		for _, id := range t.ids {
			entries = append(entries, c.byID[id])
		}
		out := c.matchEntries(img.SubImage(roi).(*image.RGBA), scale, entries)
		c.mu.Unlock()
		out.Scale, out.Profile, out.Side = scale, profile, side
		return out
	}
	var out AvatarMatch
	if locate != nil {
		out = locate()
	} else {
		out = c.Match(img, roi, scale)
	}
	out.Scale, out.Profile, out.Side = scale, profile, side
	t.ids, t.next = append(t.ids[:0], out.ids...), now.Add(500*time.Millisecond)
	return out
}

// AvatarSearchRegion is the bounded current-frame localization area for one
// selected HUD profile and side. It deliberately covers only the upper 150
// reference pixels of that side, never an arbitrary capture or full frame.
func AvatarSearchRegion(area detect.ContentArea, profile string, left bool) image.Rectangle {
	if area.W <= 0 || area.H <= 0 || (profile != "camp" && profile != "duel") {
		return image.Rectangle{}
	}
	x0, x1 := 0.0, 220.0
	if !left {
		x0, x1 = 740, 960
	}
	return image.Rect(
		area.X+int(math.Round(x0*float64(area.W)/960)),
		area.Y,
		area.X+int(math.Round(x1*float64(area.W)/960)),
		area.Y+int(math.Round(150*float64(area.H)/540)),
	)
}

// localize retains eight catalog candidates from a fixed portrait-window grid,
// then validates them with the existing full NCC search. ponytail: this is
// bounded O(242*32*36² + 8*6*220*150) per 960-wide HUD side, and AvatarTracker
// performs the catalog pass at most twice per second per profile/side.
func (c *avatarCatalog) localize(img *image.RGBA, roi image.Rectangle, scale float64, profile, side string) AvatarMatch {
	if c == nil || img == nil || scale <= 0 {
		return AvatarMatch{}
	}
	roi = roi.Intersect(img.Bounds())
	if roi.Empty() {
		return AvatarMatch{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := c.coarseEntries(avatarWindows(roi, scale), img, 8)
	out := c.matchEntries(img.SubImage(roi).(*image.RGBA), scale, entries)
	out.Scale, out.Profile, out.Side = scale, profile, side
	return out
}

func avatarWindows(roi image.Rectangle, scale float64) []image.Rectangle {
	size := max(16, int(math.Round(90*scale)))
	step := max(8, int(math.Round(24*scale)))
	starts := func(minimum, maximum int) []int {
		last := maximum - size
		if last <= minimum {
			return []int{minimum}
		}
		out := make([]int, 0, (last-minimum)/step+2)
		for x := minimum; x < last; x += step {
			out = append(out, x)
		}
		return append(out, last)
	}
	xs, ys := starts(roi.Min.X, roi.Max.X), starts(roi.Min.Y, roi.Max.Y)
	out := make([]image.Rectangle, 0, len(xs)*len(ys))
	for _, y := range ys {
		for _, x := range xs {
			out = append(out, image.Rect(x, y, x+size, y+size).Intersect(roi))
		}
	}
	return out
}

// AvatarRegion maps one normalized 960-wide reference ROI from an existing
// first-bead anchor; it remains the fixed-calibration fallback when localization
// cannot find a current portrait.
func AvatarRegion(first image.Point, scale float64, left bool) image.Rectangle {
	x0, x1 := -80.0, -14.0
	if !left {
		x0, x1 = 5, 78
	}
	return image.Rect(first.X+int(math.Round(x0*scale)), first.Y-int(math.Round(82*scale)), first.X+int(math.Round(x1*scale)), first.Y-int(math.Round(5*scale)))
}

// canonicalAvatarName maps only documented historical spellings onto a current
// canonical catalog title. It deliberately does not collapse distinct forms.
// NameRegionFromAvatar derives the exact bounded title search rectangle from
// a current avatar footprint. Its offsets intentionally match NameRegion at
// the reviewed default HUD anchor, while allowing a shifted current HUD to move
// both signals together.
func NameRegionFromAvatar(rect image.Rectangle, scale float64, left bool) image.Rectangle {
	if rect.Empty() || scale <= 0 {
		return image.Rectangle{}
	}
	y0, y1 := rect.Min.Y-int(math.Round(3*scale)), rect.Min.Y+int(math.Round(48*scale))
	if left {
		return image.Rect(rect.Max.X+int(math.Round(2*scale)), y0, rect.Max.X+int(math.Round(348*scale)), y1)
	}
	return image.Rect(rect.Min.X-int(math.Round(340*scale)), y0, rect.Min.X+int(math.Round(16*scale)), y1)
}

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
