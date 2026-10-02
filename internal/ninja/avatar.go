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
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	scaled             map[avatarSize]*match.PreparedNCC
}

// avatarSize keys the per-entry scaled template cache.
type avatarSize struct{ w, h int }

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
	// mu guards only the mutable template state: entry.gray/mask lazy decode,
	// entry.scaled, cacheScale, cachedEntries and cacheWipes. entries, byID and
	// every other entry field are written once by loadAvatarCatalog and are
	// read lock-free afterwards; prepared templates are immutable.
	mu            sync.Mutex
	entries       []*avatarEntry
	byID          map[string]*avatarEntry
	cacheScale    float64
	cachedEntries int
	// cacheWipes counts overflow wipes of the scaled template cache; a single
	// scan must never trigger one (tests assert it).
	cacheWipes int
}

type avatarFinalist struct {
	entry *avatarEntry
	score float64
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
	// Pixel memo: the last ROI pixels (packed RGBA rows) and the result they
	// produced on the given path. Matching is deterministic, so byte-identical
	// pixels with the same geometry/catalog/path/shortlist give the same
	// result; this never carries identity across differing pixels.
	memoPix  []byte
	memoFull bool
	memoIDs  []string
	memoOut  AvatarMatch
	memoOK   bool
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
	// Cheap index validation stays sequential (duplicates depend on order);
	// the per-asset read/verify/decode work runs on a bounded pool into an
	// index-ordered slice. The error reported is the one the sequential loop
	// would have stopped at: the first failing index, validation first.
	errs := make([]error, len(index.Entries))
	sums := make([]string, len(index.Entries))
	seen := make(map[string]bool, len(index.Entries))
	for i, item := range index.Entries {
		sum := item.SHA256
		if sum == "" {
			sum = item.Avatar.SHA256
		}
		if !validAvatarID(item.ID) || seen[item.ID] || len(sum) != 64 {
			errs[i] = fmt.Errorf("invalid or duplicate avatar id %q", item.ID)
		} else if _, err := hex.DecodeString(sum); err != nil {
			errs[i] = fmt.Errorf("invalid avatar sha256 for %s", item.ID)
		}
		seen[item.ID], sums[i] = true, sum
	}
	entries := make([]*avatarEntry, len(index.Entries))
	parallelFor(len(index.Entries), func(i int) {
		if errs[i] == nil {
			entries[i], errs[i] = loadAvatarEntry(index.Entries[i], sums[i], readFile)
		}
	})
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	catalog := &avatarCatalog{entries: entries, byID: make(map[string]*avatarEntry, len(entries))}
	for _, entry := range entries {
		catalog.byID[entry.id] = entry
	}
	return catalog, nil
}

// loadAvatarEntry reads, verifies and decodes one indexed asset. It touches
// only its own entry, so the catalog loader may run it concurrently.
func loadAvatarEntry(item avatarIndexEntry, sum string, readFile func(string) ([]byte, error)) (*avatarEntry, error) {
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
	// The full-resolution gray/mask are exactly what matchEntries would
	// otherwise re-decode lazily from data; keep them so a PNG is decoded once.
	return &avatarEntry{id: item.ID, name: avatarName(item), baseName: avatarBaseName(item), data: png, thumb: match.PrepareNCC(thumbGray, thumbMask), thumbGray: thumbGray, thumbMask: thumbMask, coarseGray: match.ScaleGray(gray, 36, 36), coarseMask: match.ScaleGray(mask, 36, 36), gray: gray, mask: mask}, nil
}

// parallelFor runs fn(0..n-1) on match.Workers(n) workers pulling indexes
// from a shared counter. fn must write only state owned by its index. Small n
// runs inline.
func parallelFor(n int, fn func(int)) {
	workers := match.Workers(n)
	if workers <= 1 || n <= 2 {
		for i := range n {
			fn(i)
		}
		return
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				fn(i)
			}
		})
	}
	wg.Wait()
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
	// The direct paths reproduce color.NRGBA.RGBA / color.RGBA.RGBA (16-bit
	// expansion, NRGBA alpha premultiply) and the same >>8 luma byte for byte;
	// they only avoid boxing a color per pixel. Other models use At().
	switch img := src.(type) {
	case *image.NRGBA:
		for y := 0; y < b.Dy(); y++ {
			row := img.Pix[img.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < b.Dx(); x++ {
				p := row[4*x : 4*x+4 : 4*x+4]
				a := uint32(p[3])
				r, g, bl := (uint32(p[0])|uint32(p[0])<<8)*a/0xff, (uint32(p[1])|uint32(p[1])<<8)*a/0xff, (uint32(p[2])|uint32(p[2])<<8)*a/0xff
				gray.Pix[y*gray.Stride+x] = uint8((299*(r>>8) + 587*(g>>8) + 114*(bl>>8) + 500) / 1000)
				if a|a<<8 >= 0x8000 {
					mask.Pix[y*mask.Stride+x] = 255
				}
			}
		}
		return gray, mask
	case *image.RGBA:
		for y := 0; y < b.Dy(); y++ {
			row := img.Pix[img.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < b.Dx(); x++ {
				p := row[4*x : 4*x+4 : 4*x+4]
				r, g, bl, a := uint32(p[0])|uint32(p[0])<<8, uint32(p[1])|uint32(p[1])<<8, uint32(p[2])|uint32(p[2])<<8, uint32(p[3])|uint32(p[3])<<8
				gray.Pix[y*gray.Stride+x] = uint8((299*(r>>8) + 587*(g>>8) + 114*(bl>>8) + 500) / 1000)
				if a >= 0x8000 {
					mask.Pix[y*mask.Stride+x] = 255
				}
			}
		}
		return gray, mask
	}
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
	// The coarse locator reads only entries and their load-time immutable
	// fields (id, coarseGray, coarseMask), so it runs without the catalog lock;
	// matchEntries takes it only for its template cache.
	view := img.SubImage(roi).(*image.RGBA)
	gray := match.ToGray(view)
	coarse := match.ScaleGray(gray, 36, 36)
	// The portrait is a fixed HUD element: relative to the canonical
	// AvatarRegion window its diamond center sits near (35.5, 48) reference
	// pixels and renders at roughly 0.6x the 85px catalog asset. A square crop
	// around that anchor removes the ROI's background and vertical squash from
	// the 36x36 locator. The unanchored crop stays as a fallback for callers
	// that pass a synthetic centered portrait rather than the HUD ROI.
	// gray has origin (0,0) at roi.Min; cropping it equals converting the
	// cropped RGBA because luma is per pixel.
	anchor := avatarAnchorRect(roi, scale).Intersect(roi).Sub(roi.Min)
	hudCoarse := match.ScaleGray(gray.SubImage(anchor).(*image.Gray), 36, 36)
	best := make([]avatarFinalist, 0, len(c.entries))
	for _, entry := range c.entries {
		// Two aligned 36² thumbnail queries (HUD anchor + whole ROI) keep
		// recall for both real HUD frames and centered synthetic portraits.
		// Only the retained candidates do full-resolution NCC.
		score := coarseAvatarScore(coarse, entry.coarseGray, entry.coarseMask)
		if hud := coarseAvatarScore(hudCoarse, entry.coarseGray, entry.coarseMask); hud > score {
			score = hud
		}
		best = append(best, avatarFinalist{entry, score})
	}
	sort.SliceStable(best, func(i, j int) bool {
		if best[i].score != best[j].score {
			return best[i].score > best[j].score
		}
		return best[i].entry.id < best[j].entry.id
	})
	// Coarse matching is only a locator. A real HUD portrait can be partially
	// clipped by the capture boundary and its render scale is not guaranteed to
	// equal the bead-derived scale, so the true entry may rank below the first
	// few color-similar portraits (measured worst case: 水门 at rank 10). Keep
	// this bounded recall set; strict multi-scale NCC and the score margin
	// still decide identity.
	if len(best) > 24 {
		best = best[:24]
	}
	entries := make([]*avatarEntry, 0, len(best))
	for i := range best {
		entries = append(entries, best[i].entry)
	}
	return c.matchEntries(view, gray, scale, entries)
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

// avatarFactorPrimary is the full-scan locator factor; avatarFactorsFine is
// the full sweep without it, in the same order, for the re-scored leaders.
var (
	avatarFactorPrimary = []float64{.60}
	avatarFactorsFine   = []float64{.55, .65, .70, .75, .85, 1.0, 1.15, 1.30, 1.45}
)

// gray is ToGray(view), converted once by the caller and shared by every NCC
// query instead of being rebuilt per entry/factor; nil converts it here.
//
// The caller must not hold c.mu. The lock covers only the lazy decode and the
// template cache while the jobs are built; NCC scoring runs unlocked (and in
// parallel) on immutable prepared templates and the caller's pixels. Scores
// are reduced in the original entry/factor order with the same strict '>', so
// the result equals the serial scan exactly.
func (c *avatarCatalog) matchEntries(view *image.RGBA, gray *image.Gray, scale float64, entries []*avatarEntry) AvatarMatch {
	var out AvatarMatch
	if gray == nil {
		gray = match.ToGray(view)
	}
	// Keep the best score per identity before calculating the runner-up. A
	// single identity is evaluated at several display scales; treating its
	// second scale as a different candidate can reject an otherwise unambiguous
	// portrait (best=.90, same-ID runner-up=.85).
	bestByID := make(map[string]float64, len(entries))
	nameByID := make(map[string]string, len(entries))
	baseByID := make(map[string]string, len(entries))
	bounds := view.Bounds()
	scoreAll := func(jobs []avatarJob) {
		parallelFor(len(jobs), func(i int) {
			jobs[i].score = avatarJobScore(view, gray, jobs[i].prepared)
		})
		for _, job := range jobs {
			if job.score > bestByID[job.entry.id] {
				bestByID[job.entry.id] = job.score
			}
		}
	}
	c.mu.Lock()
	c.evictTemplates(scale)
	for _, entry := range entries {
		if !c.decode(entry) {
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
		jobs := make([]avatarJob, 0, len(entries))
		for _, entry := range entries {
			jobs = c.appendJobs(jobs, entry, scale, bounds, avatarFactorPrimary)
		}
		c.mu.Unlock()
		scoreAll(jobs)
		primary := make([]stageEntry, 0, len(entries))
		for _, entry := range entries {
			if entry != nil {
				primary = append(primary, stageEntry{entry, bestByID[entry.id]})
			}
		}
		sort.SliceStable(primary, func(i, j int) bool {
			if primary[i].score != primary[j].score {
				return primary[i].score > primary[j].score
			}
			return primary[i].entry.id < primary[j].entry.id
		})
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
		// The primary factor's score is already in bestByID, exactly as a
		// repeat would compute it (same template, same pixels), and an equal
		// score never wins the strict '>', so the fine sweep skips it.
		jobs = jobs[:0]
		c.mu.Lock()
		c.evictTemplates(scale)
		for _, entry := range entries {
			if entry != nil && fine[entry.id] {
				jobs = c.appendJobs(jobs, entry, scale, bounds, avatarFactorsFine)
			}
		}
		c.mu.Unlock()
		scoreAll(jobs)
	} else {
		jobs := make([]avatarJob, 0, len(entries)*len(avatarFactorsFull))
		for _, entry := range entries {
			jobs = c.appendJobs(jobs, entry, scale, bounds, avatarFactorsFull)
		}
		c.mu.Unlock()
		scoreAll(jobs)
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

// avatarJob is one entry at one display factor. prepared is nil when the
// template is larger than the ROI: NCC cannot slide it, so the job keeps the
// zero score instead of preparing and measuring a meaningless one.
type avatarJob struct {
	entry    *avatarEntry
	prepared *match.PreparedNCC
	score    float64
}

// appendJobs fetches or prepares the entry's template at each factor, in
// factor order (caller holds mu).
func (c *avatarCatalog) appendJobs(jobs []avatarJob, entry *avatarEntry, scale float64, bounds image.Rectangle, factors []float64) []avatarJob {
	for _, factor := range factors {
		job := avatarJob{entry: entry}
		if w, h := avatarFactorSize(entry, scale, factor); w <= bounds.Dx() && h <= bounds.Dy() {
			job.prepared = c.prepared(entry, w, h)
		}
		jobs = append(jobs, job)
	}
	return jobs
}

// avatarJobScore only reads the immutable template and the caller's pixels,
// so jobs may be scored concurrently.
func avatarJobScore(view *image.RGBA, gray *image.Gray, prepared *match.PreparedNCC) float64 {
	if prepared == nil {
		return 0
	}
	score, err := (match.NCC{}).Match(match.Query{Image: view, Gray: gray, ROI: view.Bounds(), Prepared: prepared})
	if err != nil {
		return 0
	}
	return score.Value
}

// decode fills the full-resolution gray/mask lazily for entries that were not
// built by loadAvatarCatalog (caller holds mu).
func (c *avatarCatalog) decode(entry *avatarEntry) bool {
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
	key := avatarSize{w, h}
	if entry.scaled == nil {
		entry.scaled = make(map[avatarSize]*match.PreparedNCC, 8)
	} else if p, ok := entry.scaled[key]; ok {
		return p
	}
	p := match.PrepareNCC(match.ScaleGray(entry.gray, w, h), match.ScaleGray(entry.mask, w, h))
	entry.scaled[key] = p
	c.cachedEntries++
	if c.cachedEntries > c.templateCacheCap() {
		for _, e := range c.entries {
			e.scaled = nil
		}
		c.cachedEntries = 0
		c.cacheWipes++
	}
	return p
}

// templateCacheCap covers every size of one complete scan (each entry at
// every display factor) plus headroom, so a scan never wipes its own cache.
// Sizes depend only on the scale, which evictTemplates already keys.
func (c *avatarCatalog) templateCacheCap() int {
	return len(c.entries)*len(avatarFactorsFull) + 64
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
		t.memoOK = false
	}
	t.catalog, t.bounds, t.roi, t.scale = c, img.Bounds(), roi, scale
	same := t.memoOK && samePackedPix(t.memoPix, img, roi)
	if now.Before(t.next) && len(t.ids) > 0 {
		if same && !t.memoFull && slices.Equal(t.memoIDs, t.ids) {
			return t.memoOut.clone()
		}
		// byID is written only by loadAvatarCatalog, so it is read lock-free;
		// matchEntries takes c.mu for its template cache itself.
		entries := make([]*avatarEntry, 0, len(t.ids))
		for _, id := range t.ids {
			if entry := c.byID[id]; entry != nil {
				entries = append(entries, entry)
			}
		}
		out := c.matchEntries(img.SubImage(roi).(*image.RGBA), nil, scale, entries)
		t.remember(img, roi, false, out)
		return out
	}
	var out AvatarMatch
	if same && t.memoFull {
		out = t.memoOut.clone()
	} else {
		out = c.Match(img, roi, scale)
	}
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
	t.remember(img, roi, true, out)
	return out
}

// remember stores the result for the ROI pixels; ids is the shortlist the
// non-full path matched against (t.ids at call time).
func (t *AvatarTracker) remember(img *image.RGBA, roi image.Rectangle, full bool, out AvatarMatch) {
	t.memoPix = t.memoPix[:0]
	for y := roi.Min.Y; y < roi.Max.Y; y++ {
		i := img.PixOffset(roi.Min.X, y)
		t.memoPix = append(t.memoPix, img.Pix[i:i+4*roi.Dx()]...)
	}
	t.memoFull, t.memoOut, t.memoOK = full, out.clone(), true
	t.memoIDs = append(t.memoIDs[:0], t.ids...)
}

func samePackedPix(packed []byte, img *image.RGBA, roi image.Rectangle) bool {
	row := 4 * roi.Dx()
	if len(packed) != row*roi.Dy() {
		return false
	}
	for y := roi.Min.Y; y < roi.Max.Y; y++ {
		i := img.PixOffset(roi.Min.X, y)
		o := (y - roi.Min.Y) * row
		if !bytes.Equal(packed[o:o+row], img.Pix[i:i+row]) {
			return false
		}
	}
	return true
}

func (m AvatarMatch) clone() AvatarMatch {
	m.ids = slices.Clone(m.ids)
	return m
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
