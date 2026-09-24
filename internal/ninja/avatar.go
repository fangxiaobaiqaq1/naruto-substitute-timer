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
	ids       []string
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
	next time.Time
	ids  []string
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
	coarse := match.ScaleGray(match.ToGray(view), 36, 36)
	type finalist struct {
		entry *avatarEntry
		score float64
	}
	best := make([]finalist, 0, len(c.entries))
	for _, entry := range c.entries {
		// Direct masked thumbnail distance is O(36²) per entry and operates on
		// the normalized ROI itself. Unlike the old 16px NCC sliding search it
		// cannot discard a true centered portrait because of unrelated ROI
		// background. Only the retained candidates do full-resolution NCC.
		best = append(best, finalist{entry, coarseAvatarScore(coarse, entry.coarseGray, entry.coarseMask)})
	}
	sort.Slice(best, func(i, j int) bool { return best[i].score > best[j].score })
	// Coarse NCC works on 36×36 pixels and tests six display scales only;
	// retain a deliberately bounded wider recall set before strict full-size
	// NCC. This is not a 242-image full-resolution scan.
	if len(best) > 8 {
		best = best[:8]
	}
	entries := make([]*avatarEntry, len(best))
	for i := range best {
		entries[i] = best[i].entry
	}
	return c.matchEntries(view, scale, entries)
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
			score, err := (match.NCC{}).Match(match.Query{Image: view, ROI: view.Bounds(), Template: match.ScaleGray(entry.gray, w, h), Mask: match.ScaleGray(entry.mask, w, h)})
			if err != nil {
				continue
			}
			if score.Value > out.Score {
				out.RunnerUp, out.ID, out.Name, out.Score = out.Score, entry.id, entry.name, score.Value
			} else if entry.id != out.ID && score.Value > out.RunnerUp {
				out.RunnerUp = score.Value
			}
		}
	}
	out.Candidate = out.Name
	if out.Score < .78 || out.Score-out.RunnerUp < .08 {
		out.ID, out.Name = "", ""
	}
	return out
}

func (t *AvatarTracker) Read(c *avatarCatalog, img *image.RGBA, roi image.Rectangle, scale float64, now time.Time) AvatarMatch {
	if c == nil || img == nil {
		return AvatarMatch{}
	}
	if now.Before(t.next) && len(t.ids) > 0 {
		roi = roi.Intersect(img.Bounds())
		if roi.Empty() {
			return AvatarMatch{}
		}
		entries := make([]*avatarEntry, 0, len(t.ids))
		c.mu.Lock()
		for _, id := range t.ids {
			entries = append(entries, c.byID[id])
		}
		out := c.matchEntries(img.SubImage(roi).(*image.RGBA), scale, entries)
		c.mu.Unlock()
		return out
	}
	out := c.Match(img, roi, scale)
	t.ids, t.next = append(t.ids[:0], out.ids...), now.Add(500*time.Millisecond)
	return out
}

// AvatarRegion maps one normalized 960-wide reference ROI from an existing
// first-bead anchor; it works for 720p..1440p and letterboxed content areas.
func AvatarRegion(first image.Point, scale float64, left bool) image.Rectangle {
	x0, x1 := -80.0, -14.0
	if !left {
		x0, x1 = 5, 78
	}
	return image.Rect(first.X+int(math.Round(x0*scale)), first.Y-int(math.Round(82*scale)), first.X+int(math.Round(x1*scale)), first.Y-int(math.Round(5*scale)))
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
