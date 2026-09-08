package identity

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/match"
)

const (
	AssetDir            = "assets/identity"
	SeenDir             = "assets/identity/seen"
	FightDir            = "assets/identity/fight"
	minScore            = 0.72
	minGap              = 0.08
	fightMinScore       = 0.80
	recognitionInterval = 500 * time.Millisecond
)

var (
	mineMu       sync.RWMutex
	mineSet      = map[string]bool{}
	mineRevision uint64
)

// SetMineNames 设置里改我方名字后立刻用于认边，不用重启。
func SetMineNames(names []string) {
	next := map[string]bool{}
	for _, n := range names {
		if n = NormalizeName(n); n != "" {
			next[n] = true
		}
	}
	mineMu.Lock()
	mineSet = next
	mineRevision++
	mineMu.Unlock()
}

// The map is immutable after publication. Readers can keep a snapshot while
// settings replace it; explicit Read/Guess books do not depend on global state.
func mineSnapshot() (map[string]bool, uint64) {
	mineMu.RLock()
	defer mineMu.RUnlock()
	return mineSet, mineRevision
}

type sideROIs struct{ Left, Right config.NormalizedRect }

// DefaultROIs 是 VS / 死亡换人底栏左右账号区域（内容区归一化）。
var DefaultROIs = sideROIs{
	Left:  config.NormalizedRect{X: 0.06, Y: 0.88, Width: 0.22, Height: 0.08},
	Right: config.NormalizedRect{X: 0.72, Y: 0.88, Width: 0.22, Height: 0.08},
}

// FightROIs contain the account names in the top battle HUD, not VS's footer.
var FightROIs = sideROIs{
	Left:  config.NormalizedRect{X: 0.10, Y: 0.02, Width: 0.34, Height: 0.055},
	Right: config.NormalizedRect{X: 0.56, Y: 0.02, Width: 0.34, Height: 0.055},
}

// Named 是一张账号名模板。
type Named struct {
	Name  string
	Image image.Image
	Mine  bool
	Scene string // "fight" for top HUD crops; empty/"vs" for footer crops
}

// Readout 是一次 VS / 换人 / 对局画面上的认人结果。
type Readout struct {
	Side string // left / right / ""
	Mine string
	Opp  string
}

// LoadBook 读账号名图鉴。
// 我方：config.ui.playerNames 里填写的名字，对应 assets/identity/<名字>.png。
// 对面：同目录里其余 PNG，以及 seen/ 里曾经裁过的图。
func LoadBook(cfg config.Config) []Named {
	mine := map[string]bool{}
	for _, n := range cfg.UI.PlayerNames {
		if n = NormalizeName(n); n != "" {
			mine[n] = true
		}
	}
	var out []Named
	seen := map[string]bool{}
	addFile := func(path, label, scene string) {
		if seen[path] {
			return
		}
		img, err := loadImage(path)
		if err != nil || img == nil {
			return
		}
		seen[path] = true
		if label == "" {
			label = labelFromFile(path)
		}
		if label == "" || label == "seen" {
			return
		}
		out = append(out, Named{Name: label, Image: img, Mine: mine[label], Scene: scene})
	}

	scanDir := func(dir, scene string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			low := strings.ToLower(e.Name())
			if !strings.HasSuffix(low, ".png") && !strings.HasSuffix(low, ".jpg") && !strings.HasSuffix(low, ".jpeg") {
				continue
			}
			addFile(filepath.Join(dir, e.Name()), labelFromFile(e.Name()), scene)
		}
	}
	scanDir(AssetDir, "vs")
	scanDir(SeenDir, "vs")
	scanDir(FightDir, "fight")
	return out
}

func labelFromFile(name string) string {
	base := filepath.Base(name)
	ext := filepath.Ext(base)
	base = strings.TrimSuffix(base, ext)
	if i := strings.Index(base, "_"); i > 0 {
		base = base[:i]
	}
	return NormalizeName(base)
}

func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

func mineOf(book []Named) []Named {
	var out []Named
	for _, n := range book {
		if n.Mine {
			out = append(out, n)
		}
	}
	return out
}

func othersOf(book []Named) []Named {
	var out []Named
	for _, n := range book {
		if !n.Mine && n.Name != "" {
			out = append(out, n)
		}
	}
	return out
}

// Guesser recognizes accounts only in confirmed VS or battle scenes. Account
// changes reload the book, including templates added after startup.
func Guesser(cfg config.Config) func(*image.RGBA, string) Readout {
	SetMineNames(cfg.UI.PlayerNames)
	var book []Named
	var revision uint64
	var lastRead time.Time
	var lastScene string
	var lastBounds image.Rectangle
	mode, err := detect.ParseMode(cfg.Layout.ContentMode)
	if err != nil {
		mode = detect.ModeAuto
	}
	return func(img *image.RGBA, scene string) Readout {
		if img == nil || (scene != "fight" && scene != "vs") {
			lastScene = ""
			return Readout{}
		}
		names, current := mineSnapshot()
		if revision != current {
			cfg.UI.PlayerNames = nil
			for name := range names {
				cfg.UI.PlayerNames = append(cfg.UI.PlayerNames, name)
			}
			book = LoadBook(cfg)
			revision = current
			lastRead = time.Time{}
		}
		if len(names) == 0 || len(book) == 0 {
			return Readout{}
		}
		now := time.Now()
		if scene == lastScene && img.Bounds() == lastBounds && now.Sub(lastRead) < recognitionInterval {
			// No new evidence. Do not manufacture repeated confirmations from a cache.
			return Readout{}
		}
		lastRead, lastScene, lastBounds = now, scene, img.Bounds()
		if scene == "fight" {
			return ReadFight(img, book, mode)
		}
		r := Read(img, book, mode)
		if r.Side != "" && r.Opp == "" {
			go saveOppCrop(img, opposite(r.Side), mode)
		}
		return r
	}
}

// Read 比较左右底栏和名册。
func Read(img *image.RGBA, book []Named, mode detect.ContentMode) Readout {
	return readNames(img, bookForScene(book, "vs"), mode, DefaultROIs, minScore, 0)
}

// ReadFight recognizes account-name-only templates from the battle HUD.
func ReadFight(img *image.RGBA, book []Named, mode detect.ContentMode) Readout {
	// Bound matching cost at high capture resolutions. Only the two name strips
	// are converted/scaled, never a whole 1080p/4K frame for each template.
	return readNames(img, bookForScene(book, "fight"), mode, FightROIs, fightMinScore, 960)
}

func bookForScene(book []Named, scene string) []Named {
	var out []Named
	for _, n := range book {
		if n.Scene == scene || (scene == "vs" && n.Scene == "") {
			out = append(out, n)
		}
	}
	return out
}

func readNames(img *image.RGBA, book []Named, mode detect.ContentMode, rois sideROIs, threshold float64, maxWidth int) Readout {
	var out Readout
	if img == nil || len(book) == 0 {
		return out
	}
	ca, ok := detect.ResolveContentArea(img, mode, detect.LogicWidth, detect.LogicHeight, 0.015)
	if !ok {
		return out
	}
	mine := mineOf(book)
	// An opponent (or an unlabeled seen crop) is never a fallback for "me".
	if len(mine) == 0 {
		return out
	}
	leftHit := bestHit(img, ca, rois.Left, mine, maxWidth)
	rightHit := bestHit(img, ca, rois.Right, mine, maxWidth)
	switch {
	case leftHit.Score >= threshold && leftHit.Score >= rightHit.Score+minGap:
		out.Side, out.Mine = "left", leftHit.Name
	case rightHit.Score >= threshold && rightHit.Score >= leftHit.Score+minGap:
		out.Side, out.Mine = "right", rightHit.Name
	}
	if out.Side == "" {
		return out
	}
	oppROI := rois.Right
	if out.Side == "right" {
		oppROI = rois.Left
	}
	oppHit := bestHit(img, ca, oppROI, othersOf(book), maxWidth)
	if oppHit.Score >= threshold {
		out.Opp = oppHit.Name
	}
	return out
}

// Guess 兼容旧测试：只返回我方在哪一边。
func Guess(img *image.RGBA, names []image.Image, mode detect.ContentMode) string {
	book := make([]Named, 0, len(names))
	for _, n := range names {
		book = append(book, Named{Name: "me", Image: n, Mine: true})
	}
	return Read(img, book, mode).Side
}

type hit struct {
	Name  string
	Score float64
}

func bestHit(img *image.RGBA, ca detect.ContentArea, roi config.NormalizedRect, names []Named, maxWidth int) hit {
	var best hit
	if len(names) == 0 {
		return best
	}
	search := match.CropRGBA(img, mapRect(ca, roi))
	if search == nil || search.Bounds().Empty() {
		return best
	}
	gray := match.ToGray(search)
	scale := 1.0
	if maxWidth > 0 && ca.W > maxWidth {
		scale = float64(maxWidth) / float64(ca.W)
		w := int(math.Round(float64(gray.Bounds().Dx()) * scale))
		h := int(math.Round(float64(gray.Bounds().Dy()) * scale))
		gray = match.ScaleGray(gray, w, h)
		search = image.NewRGBA(gray.Bounds()) // Query.Gray supplies pixels, Image supplies bounds.
	}
	for _, n := range names {
		if n.Image == nil {
			continue
		}
		tw := int(math.Round(float64(n.Image.Bounds().Dx()) * float64(ca.W) / detect.LogicWidth * scale))
		th := int(math.Round(float64(n.Image.Bounds().Dy()) * float64(ca.H) / detect.LogicHeight * scale))
		if tw < 8 || th < 8 || tw > gray.Bounds().Dx() || th > gray.Bounds().Dy() {
			continue
		}
		templ := match.ScaleGray(match.ToGray(n.Image), tw, th)
		var mask *image.Gray
		if maxWidth > 0 {
			mask = accountGlyphMask(templ)
		}
		score, err := (match.NCC{}).Match(match.Query{Image: search, Gray: gray, ROI: search.Bounds(), Template: templ, Mask: mask})
		// Preserve the original full-patch path where downsampling leaves too
		// little glyph/outline detail for the mask. Neither threshold is lowered.
		if mask != nil && (err != nil || score.Value < fightMinScore) {
			full, fullErr := (match.NCC{}).Match(match.Query{Image: search, Gray: gray, ROI: search.Bounds(), Template: templ})
			if fullErr == nil && (err != nil || full.Value > score.Value) {
				score, err = full, nil
			}
		}
		// Low-resolution font/ROI rounding can put the glyph patch one pixel
		// away from its nominal width/height. Refine only an already plausible
		// location, not nine new full-strip scans and not a lower threshold.
		if mask != nil && err == nil && score.Value >= .65 && score.Value < fightMinScore {
			peak := score.Peak
			for dw := -1; dw <= 1; dw++ {
				for dh := -1; dh <= 1; dh++ {
					if dw == 0 && dh == 0 {
						continue
					}
					adjusted := match.ScaleGray(match.ToGray(n.Image), tw+dw, th+dh)
					roi := image.Rectangle{Min: peak, Max: peak.Add(adjusted.Bounds().Size())}.Inset(-2).Intersect(search.Bounds())
					refined, e := (match.NCC{}).Match(match.Query{Image: search, Gray: gray, ROI: roi, Template: adjusted, Mask: accountGlyphMask(adjusted)})
					if e == nil && refined.Value > score.Value {
						score = refined
					}
				}
			}
		}
		if err == nil && score.Value > best.Score {
			best = hit{Name: n.Name, Score: score.Value}
		}
	}
	return best
}

func mapRect(ca detect.ContentArea, r config.NormalizedRect) image.Rectangle {
	x0, y0 := ca.Map(r.X*detect.LogicWidth, r.Y*detect.LogicHeight)
	x1, y1 := ca.Map((r.X+r.Width)*detect.LogicWidth, (r.Y+r.Height)*detect.LogicHeight)
	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}
	return image.Rect(x0, y0, x1, y1)
}

func opposite(side string) string {
	if side == "left" {
		return "right"
	}
	return "left"
}

var saveMu sync.Mutex
var lastSave time.Time

func saveOppCrop(img *image.RGBA, side string, mode detect.ContentMode) {
	saveMu.Lock()
	defer saveMu.Unlock()
	if time.Since(lastSave) < 8*time.Second {
		return
	}
	if img == nil {
		return
	}
	if err := os.MkdirAll(SeenDir, 0o755); err != nil {
		return
	}
	ca, ok := detect.ResolveContentArea(img, mode, detect.LogicWidth, detect.LogicHeight, 0.015)
	if !ok {
		return
	}
	roi := DefaultROIs.Left
	if side == "right" {
		roi = DefaultROIs.Right
	}
	crop := match.CropRGBA(img, mapRect(ca, roi))
	if crop == nil || crop.Bounds().Dx() < 8 {
		return
	}
	path := filepath.Join(SeenDir, fmt.Sprintf("opp-%s.png", time.Now().Format("20060102-150405")))
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	if err := png.Encode(f, crop); err != nil {
		return
	}
	lastSave = time.Now()
}

// NormalizeName 去掉空白和路径字符，方便当文件名、当配置项。
func NormalizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsSpace(r) || r == '/' || r == '\\' || r == ':' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// MineNames returns an owned snapshot for optional text recognizers. Changes in
// the settings window never leave a background job bound to a stale account.
func MineNames() []string {
	names, _ := mineSnapshot()
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	return out
}
