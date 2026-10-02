// Package scene 用模板清单判断当前画面属于哪个场景。
// 场景名、ROI、阈值全部来自资源文件，代码里不写死「决斗场」。
package scene

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"math"
	"narutotimer/assets"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/match"
)

const DefaultManifest = "assets/templates/manifest.json"

type Manifest struct {
	MinimumRegions map[string]int `json:"minimumRegions,omitempty"`
	Reference      struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"reference"`
	Templates []TemplateSpec `json:"templates"`
}

type TemplateSpec struct {
	TemplateScale    float64               `json:"templateScale,omitempty"`
	ContinuationOnly bool                  `json:"continuationOnly,omitempty"`
	RoundOpening     bool                  `json:"roundOpening,omitempty"`
	LayoutProfile    string                `json:"layoutProfile,omitempty"`
	ID               string                `json:"id"`
	File             string                `json:"file"`
	Mask             string                `json:"mask,omitempty"`
	Scene            string                `json:"scene"`
	ROI              config.NormalizedRect `json:"roi"`
	Threshold        float64               `json:"threshold"`
}

type template struct {
	spec TemplateSpec
	gray *image.Gray
	mask *image.Gray
	refW int
	refH int
}

// Prepared images are immutable after construction. Only the current content
// geometry is retained so resizing cannot grow the cache indefinitely.
type preparedTemplate struct {
	spec       TemplateSpec
	roi        image.Rectangle
	gray, mask *image.Gray
	ncc        *match.PreparedNCC
	index      int  // Position in Catalog.templates; keys the peak hint.
	windowed   bool // ROI narrowed to the remembered peak; a miss keeps the hint.
}

// Catalog 是已加载的模板库。
type Catalog struct {
	minimumRegions map[string]int
	templates      []template
	cfg            config.SceneConfig
	mode           detect.ContentMode
	layoutCfg      config.LayoutConfig
	matcher        match.Matcher
	fightSet       map[string]bool
	endSet         map[string]bool
	holdSet        map[string]bool
	prepareMu      sync.Mutex
	preparedArea   detect.ContentArea
	prepared       []preparedTemplate
	// hints remember where each template was last accepted for the current
	// geometry. HUD markers do not move, so a small window around that peak
	// is checked first; only a miss falls back to the full ROI scan.
	hintMu sync.Mutex
	hints  []templateHint
	// Cold templates (no remembered peak) are rescanned on every frame unless
	// a fight marker is confirmed on this frame and a full scan ran within the
	// last coldScanEvery frames and coldScanInterval. Their last scores are
	// reused in between; a template that was present would hold a hint.
	coldAt     time.Time
	coldFrames int
	lastCold   []Hit
}

const (
	coldScanEvery    = 3
	coldScanInterval = 100 * time.Millisecond
)

type templateHint struct {
	peak  image.Point
	valid bool
}

// hintRadius bounds the window checked around a remembered peak, in pixels.
const hintRadius = 2

// Load 默认读取随 EXE 更新的内置资源，显式自定义路径才读取外置 manifest。
// 自定义文件不存在或 templates 为空时返回 (nil, nil)，由上层回退旧门闩。
func Load(cfg config.Config) (*Catalog, error) {
	sc := cfg.Scene
	if !sc.Enabled {
		return nil, nil
	}
	path := sc.Manifest
	if path == "" {
		path = DefaultManifest
	}
	// A leftover assets directory from an older ZIP must not override a newer EXE.
	// Keep other paths external so custom calibration remains an explicit option.
	embedded := filepath.Clean(path) == filepath.Clean(DefaultManifest)
	var data []byte
	var err error
	if embedded {
		data, err = assets.Templates.ReadFile("templates/manifest.json")
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scene: read manifest %q: %w", path, err)
	}
	var man Manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return nil, fmt.Errorf("scene: decode manifest %q: %w", path, err)
	}
	for scene, n := range man.MinimumRegions {
		if scene == "" || n < 1 || n > 8 {
			return nil, fmt.Errorf("scene: invalid minimumRegions %q=%d", scene, n)
		}
	}
	refW, refH := man.Reference.Width, man.Reference.Height
	if refW <= 0 {
		refW = cfg.Layout.ReferenceWidth
	}
	if refH <= 0 {
		refH = cfg.Layout.ReferenceHeight
	}
	if refW <= 0 {
		refW = detect.LogicWidth
	}
	if refH <= 0 {
		refH = detect.LogicHeight
	}
	dir := filepath.Dir(path)
	readGray := func(name string) (*image.Gray, error) {
		if !embedded {
			return loadGray(filepath.Join(dir, name))
		}
		data, err := assets.Templates.ReadFile("templates/" + filepath.ToSlash(name))
		if err != nil {
			return nil, err
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		return match.ToGray(img), nil
	}
	out := &Catalog{
		minimumRegions: man.MinimumRegions,
		cfg:            sc,
		mode:           parseMode(cfg.Layout.ContentMode),
		layoutCfg:      cfg.Layout,
		matcher:        match.NCC{},
		fightSet:       map[string]bool{},
		endSet:         map[string]bool{},
		holdSet:        map[string]bool{},
	}
	for _, id := range sc.FightScenes {
		out.fightSet[id] = true
	}
	for _, id := range sc.EndScenes {
		out.endSet[id] = true
	}
	for _, id := range sc.HoldScenes {
		out.holdSet[id] = true
	}
	for _, spec := range man.Templates {
		if spec.TemplateScale != 0 && (spec.TemplateScale < 0.5 || spec.TemplateScale > 2) {
			return nil, fmt.Errorf("scene: template %s: templateScale must be between 0.5 and 2", spec.ID)
		}
		if spec.LayoutProfile != "" && spec.LayoutProfile != "camp" && spec.LayoutProfile != "duel" {
			return nil, fmt.Errorf("scene: template %s: unsupported layoutProfile %q", spec.ID, spec.LayoutProfile)
		}
		if spec.RoundOpening && (!out.fightSet[spec.Scene] || spec.LayoutProfile == "") {
			return nil, fmt.Errorf("scene: template %s: roundOpening requires a fight scene and layout profile", spec.ID)
		}
		if spec.ID == "" || spec.File == "" || spec.Scene == "" {
			return nil, fmt.Errorf("scene: template missing id/file/scene")
		}
		if spec.Threshold <= 0 {
			spec.Threshold = sc.MinFightScore
			if !out.fightSet[spec.Scene] && sc.MinOtherScore > 0 {
				spec.Threshold = sc.MinOtherScore
			}
			if spec.Threshold <= 0 {
				spec.Threshold = 0.82
			}
		}
		gray, err := readGray(spec.File)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("scene: template %s: %w", spec.ID, err)
		}
		var mask *image.Gray
		if spec.Mask != "" {
			mask, err = readGray(spec.Mask)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("scene: mask %s: %w", spec.ID, err)
			}
		}
		out.templates = append(out.templates, template{
			spec: spec, gray: gray, mask: mask, refW: refW, refH: refH,
		})
	}
	if len(out.templates) == 0 {
		return nil, nil
	}
	return out, nil
}

func parseMode(s string) detect.ContentMode {
	mode, err := detect.ParseMode(s)
	if err != nil {
		return detect.ModeAuto
	}
	return mode
}

func loadGray(path string) (*image.Gray, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return match.ToGray(img), nil
}

// Decide 实现 engine.Gate。
func (c *Catalog) Decide(img *image.RGBA) engine.GateDecision {
	return c.DecideAt(img, time.Now())
}

// DecideAt implements engine.TimedGate. Acquisition time bounds how long
// templates without a remembered peak may go unscanned during a confirmed fight.
func (c *Catalog) DecideAt(img *image.RGBA, at time.Time) engine.GateDecision {
	if c == nil || img == nil {
		return engine.GateDecision{Kind: engine.GateUncertain}
	}
	if blank, kind := lumaBlank(img); blank {
		return engine.GateDecision{Kind: kind, SceneID: "blank"}
	}
	ca, supported := c.contentArea(img)
	if !supported {
		return engine.GateDecision{Kind: engine.GateUncertain, SceneID: "unsupported-resolution"}
	}
	if at.IsZero() {
		at = time.Now()
	}
	prepared := c.prepare(ca)
	hits := c.scoreAll(img, prepared, at)
	var bestFight, bestOther float64
	var bestRaw float64
	profileScores := map[string]float64{}
	otherRegions := map[string][]image.Rectangle{}
	var fightID, otherID string
	var fightLayout string
	var openingScore float64
	var openingLayout string
	for i, t := range prepared {
		if t.spec.ContinuationOnly {
			continue
		}
		hit := hits[i]
		bestRaw = max(bestRaw, hit.Value)
		if hit.Value < t.spec.Threshold {
			continue
		}
		if c.fightSet[t.spec.Scene] {
			profileScores[t.spec.LayoutProfile] = max(profileScores[t.spec.LayoutProfile], hit.Value)
			if t.spec.RoundOpening && hit.Value > openingScore {
				openingScore, openingLayout = hit.Value, t.spec.LayoutProfile
			}
			if hit.Value > bestFight {
				bestFight, fightID, fightLayout = hit.Value, t.spec.Scene, t.spec.LayoutProfile
			}
		} else {
			independent := true
			for _, previous := range otherRegions[t.spec.Scene] {
				overlap := previous.Intersect(t.roi)
				if overlap.Dx()*overlap.Dy()*4 > min(previous.Dx()*previous.Dy(), t.roi.Dx()*t.roi.Dy()) {
					independent = false
				}
			}
			if independent {
				otherRegions[t.spec.Scene] = append(otherRegions[t.spec.Scene], t.roi)
			}
			if hit.Value > bestOther {
				bestOther, otherID = hit.Value, t.spec.Scene
			}
		}
		// Scan competing templates before selecting a scene or coordinate profile.
	}
	minFight := c.cfg.MinFightScore
	minOther := c.cfg.MinOtherScore
	margin := c.cfg.Margin
	if minFight <= 0 {
		minFight = 0.82
	}
	if minOther <= 0 {
		minOther = 0.85
	}
	if margin < 0 {
		margin = 0
	}

	if fightID != "" && bestFight >= minFight && bestFight >= bestOther+margin {
		for profile, score := range profileScores {
			if profile != "" && profile != fightLayout && score >= minFight && bestFight-score <= margin {
				return engine.GateDecision{Kind: engine.GateUncertain, Confidence: bestFight}
			}
		}
		return engine.GateDecision{
			Kind:          engine.GateFight,
			SceneID:       fightID,
			Confidence:    bestFight,
			LayoutProfile: fightLayout,
			// The opening marker is a second accepted fight template, not a
			// heuristic inferred from bean colors. Do not apply it to a different
			// calibrated HUD profile.
			RoundOpening: openingScore > 0 && openingLayout == fightLayout,
		}
	}
	// Result/selection overlays can leave the round label visible behind them.
	// Two separate accepted page controls establish the foreground page even
	// when the old label is within the normal ambiguity margin.
	pageConsensus := len(otherRegions[otherID]) >= 2 && bestOther >= bestFight
	if otherID != "" && bestOther >= minOther && (bestOther >= bestFight+margin || pageConsensus) {
		// A cutscene can resemble one result label. Only independently located
		// page controls satisfy a multi-region rule; duplicate templates do not.
		// Insufficient evidence holds observation, never resets the match.
		if len(otherRegions[otherID]) < max(1, c.minimumRegions[otherID]) {
			return engine.GateDecision{Kind: engine.GateUncertain, Confidence: bestOther}
		}
		kind := engine.GateNotFight
		if c.holdSet[otherID] || !c.endSet[otherID] {
			// 死亡换人 / VS / 选人 / 匹配：对局没打完，豆和钟必须保住。
			kind = engine.GateUncertain
		}
		return engine.GateDecision{Kind: kind, SceneID: otherID, Confidence: bestOther}
	}
	return engine.GateDecision{Kind: engine.GateUncertain, Confidence: bestRaw}
}

func (c *Catalog) hasOther() bool {
	for _, t := range c.templates {
		if !c.fightSet[t.spec.Scene] {
			return true
		}
	}
	return false
}

// Hit 是单条模板的分数，供 CLI 打印。
type Hit struct {
	ID    string
	Scene string
	Value float64
}

// ScoreAll 返回每条模板的分数，不表决。
func (c *Catalog) ScoreAll(img *image.RGBA) []Hit {
	if c == nil || img == nil {
		return nil
	}
	ca, supported := c.contentArea(img)
	if !supported {
		return nil
	}
	views := make(map[image.Rectangle]*image.Gray)
	out := make([]Hit, 0, len(c.templates))
	for _, t := range c.prepare(ca) {
		roi := t.roi.Intersect(img.Bounds())
		if roi.Empty() || t.gray == nil {
			out = append(out, Hit{ID: t.spec.ID, Scene: t.spec.Scene})
			continue
		}
		view := img.SubImage(roi).(*image.RGBA)
		gray, ok := views[roi]
		if !ok {
			gray = match.ToGray(view)
			views[roi] = gray
		}
		out = append(out, c.scoreOne(view, gray, t))
	}
	return out
}

func (c *Catalog) contentArea(img *image.RGBA) (detect.ContentArea, bool) {
	w, h := c.layoutCfg.ReferenceWidth, c.layoutCfg.ReferenceHeight
	if w <= 0 || h <= 0 {
		w, h = detect.LogicWidth, detect.LogicHeight
	}
	return detect.ResolveContentArea(img, c.mode, w, h, c.layoutCfg.AutoAspectTolerance)
}

// SupportsFight checks a separate, static battle control. This evidence cannot
// establish a scene on its own; Gated also requires a previously identified
// profile and fresh readable beads in both HUD corners.
func (c *Catalog) SupportsFight(img *image.RGBA, profile string) bool {
	ca, supported := c.contentArea(img)
	if !supported {
		return false
	}
	score := c.scorer(img)
	for _, t := range c.prepare(ca) {
		if !t.spec.ContinuationOnly || (t.spec.LayoutProfile != "" && t.spec.LayoutProfile != profile) {
			continue
		}
		if score(t).Value >= t.spec.Threshold {
			return true
		}
	}
	return false
}

func (c *Catalog) prepare(ca detect.ContentArea) []preparedTemplate {
	c.prepareMu.Lock()
	defer c.prepareMu.Unlock()
	if c.prepared != nil && c.preparedArea == ca {
		return c.prepared
	}
	out := make([]preparedTemplate, len(c.templates))
	for i, t := range c.templates {
		out[i] = prepareOne(ca, t)
		out[i].index = i
	}
	c.preparedArea, c.prepared = ca, out
	c.hintMu.Lock()
	c.hints = make([]templateHint, len(out)) // Peaks belong to one geometry only.
	c.lastCold = make([]Hit, len(out))
	c.coldAt, c.coldFrames = time.Time{}, 0
	c.hintMu.Unlock()
	return out
}

// coldScanDue reports whether templates without a remembered peak must be
// searched on this frame. Time moving backwards (replay seek) always rescans.
func (c *Catalog) coldScanDue(at time.Time, hotFight bool) bool {
	c.hintMu.Lock()
	defer c.hintMu.Unlock()
	if !hotFight || c.coldAt.IsZero() || at.Before(c.coldAt) || at.Sub(c.coldAt) >= coldScanInterval || c.coldFrames >= coldScanEvery {
		return true
	}
	c.coldFrames++
	return false
}

func (c *Catalog) recordColdScan(at time.Time, hits []Hit) {
	c.hintMu.Lock()
	defer c.hintMu.Unlock()
	c.coldAt, c.coldFrames = at, 0
	if len(hits) == len(c.lastCold) {
		copy(c.lastCold, hits)
	}
}

func (c *Catalog) lastColdHit(index int) Hit {
	c.hintMu.Lock()
	defer c.hintMu.Unlock()
	if index < 0 || index >= len(c.lastCold) {
		return Hit{}
	}
	return c.lastCold[index]
}

func (c *Catalog) hint(index int) (templateHint, bool) {
	c.hintMu.Lock()
	defer c.hintMu.Unlock()
	if index < 0 || index >= len(c.hints) {
		return templateHint{}, false
	}
	return c.hints[index], c.hints[index].valid
}

func (c *Catalog) setHint(index int, peak image.Point, valid bool) {
	c.hintMu.Lock()
	defer c.hintMu.Unlock()
	if index < 0 || index >= len(c.hints) {
		return
	}
	c.hints[index] = templateHint{peak: peak, valid: valid}
}

func prepareOne(ca detect.ContentArea, t template) preparedTemplate {
	roi := mapRect(ca, t.spec.ROI)
	out := preparedTemplate{spec: t.spec, roi: roi}
	scale := t.spec.TemplateScale
	if scale == 0 {
		scale = 1
	}
	tw := int(math.Round(float64(t.gray.Bounds().Dx()) * float64(ca.W) / float64(t.refW) * scale))
	th := int(math.Round(float64(t.gray.Bounds().Dy()) * float64(ca.H) / float64(t.refH) * scale))
	if tw < 4 {
		tw = 4
	}
	if th < 4 {
		th = 4
	}
	if tw > roi.Dx() {
		tw = roi.Dx()
	}
	if th > roi.Dy() {
		th = roi.Dy()
	}
	if tw < 4 || th < 4 {
		return out
	}
	out.gray = match.ScaleGray(t.gray, tw, th)
	if t.mask != nil {
		out.mask = match.ScaleGray(t.mask, tw, th)
	}
	out.ncc = match.PrepareNCC(out.gray, out.mask)
	return out
}

// scoreAll scores every primary template for one frame. Templates with a
// remembered peak are checked first at that peak; when one of them confirms a
// fight marker, the remaining cold templates are rescanned only on a bounded
// cadence and otherwise keep their last full-scan score.
//
// Each stage may score its templates concurrently: a template writes only its
// own slot and its own hint, and every reduction runs afterwards in template
// order, so hits, hints and cold-scan bookkeeping match a sequential pass.
func (c *Catalog) scoreAll(img *image.RGBA, prepared []preparedTemplate, at time.Time) []Hit {
	score := c.scorer(img)
	hits := make([]Hit, len(prepared))
	done := make([]bool, len(prepared))
	work := make([]int, 0, len(prepared))
	for i, t := range prepared {
		if !t.spec.ContinuationOnly {
			if _, ok := c.hint(t.index); ok {
				work = append(work, i)
			}
		}
	}
	c.forEach(len(work), func(k int) {
		i := work[k]
		hits[i], done[i] = c.scoreAtHint(img, score, prepared[i])
	})
	hotFight := false
	for i, t := range prepared {
		if done[i] && c.fightSet[t.spec.Scene] {
			hotFight = true
		}
	}
	if !c.coldScanDue(at, hotFight) {
		for i, t := range prepared {
			if !done[i] && !t.spec.ContinuationOnly {
				hits[i] = c.lastColdHit(i)
			}
		}
		return hits
	}
	work = work[:0]
	for i, t := range prepared {
		if !done[i] && !t.spec.ContinuationOnly {
			work = append(work, i)
		}
	}
	c.forEach(len(work), func(k int) {
		i := work[k]
		hits[i] = score(prepared[i])
	})
	cold := make([]Hit, len(prepared))
	for i := range prepared {
		if !done[i] {
			cold[i] = hits[i]
		}
	}
	c.recordColdScan(at, cold)
	return hits
}

// parallelScoring reports whether templates may be scored concurrently. Only
// the stateless built-in NCC qualifies; injected matchers (tests, recorders)
// may depend on call order and always run sequentially.
func (c *Catalog) parallelScoring() bool {
	_, ok := c.matcher.(match.NCC)
	return ok
}

// forEach calls fn for 0..n-1 on a bounded worker pool, or inline when the
// matcher must stay sequential or there is too little work to share.
func (c *Catalog) forEach(n int, fn func(int)) {
	workers := match.Workers(n)
	if workers < 2 || !c.parallelScoring() {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	var next atomic.Int64
	run := func() {
		for {
			i := int(next.Add(1) - 1)
			if i >= n {
				return
			}
			fn(i)
		}
	}
	var wg sync.WaitGroup
	for range workers - 1 {
		wg.Go(run)
	}
	run() // The caller is one of the workers.
	wg.Wait()
}

// scorer converts only searched rectangles, not the animation/background of
// an entire frame. Variants with the same ROI share one gray image. Every view
// belongs to this call; concurrent catalogs cannot race on reused pixel buffers.
// The view map is shared by parallel template scoring; a view converted twice
// for the same ROI holds identical pixels, so either copy yields the same score.
func (c *Catalog) scorer(img *image.RGBA) func(preparedTemplate) Hit {
	type grayView struct {
		img  *image.RGBA
		gray *image.Gray
	}
	var mu sync.Mutex
	views := make(map[image.Rectangle]grayView)
	return func(t preparedTemplate) Hit {
		roi := t.roi.Intersect(img.Bounds())
		if roi.Empty() || t.gray == nil {
			return Hit{ID: t.spec.ID, Scene: t.spec.Scene}
		}
		mu.Lock()
		view, ok := views[roi]
		mu.Unlock()
		if !ok {
			view.img = img.SubImage(roi).(*image.RGBA)
			view.gray = match.ToGray(view.img)
			mu.Lock()
			if prior, ok := views[roi]; ok {
				view = prior
			} else {
				views[roi] = view
			}
			mu.Unlock()
		}
		return c.scoreHinted(view.img, view.gray, t)
	}
}

// scoreAtHint checks only the remembered peak window. A template that still
// scores at or above its threshold there is present and its peak is refreshed;
// the boolean is false when there is no hint or the window misses.
func (c *Catalog) scoreAtHint(img *image.RGBA, score func(preparedTemplate) Hit, t preparedTemplate) (Hit, bool) {
	hint, ok := c.hint(t.index)
	if !ok || t.gray == nil {
		return Hit{}, false
	}
	roi := t.roi.Intersect(img.Bounds())
	if roi.Empty() {
		return Hit{}, false
	}
	size := t.gray.Bounds().Size()
	window := image.Rectangle{Min: hint.peak.Sub(image.Pt(hintRadius, hintRadius)), Max: hint.peak.Add(image.Pt(hintRadius, hintRadius)).Add(size)}.Intersect(roi)
	if window.Dx() < size.X || window.Dy() < size.Y {
		return Hit{}, false
	}
	windowed := t
	windowed.roi, windowed.windowed = window, true
	hit := score(windowed)
	if hit.Value < t.spec.Threshold {
		return Hit{}, false
	}
	return hit, true
}

// scoreHinted searches the whole ROI and remembers the peak when the template
// is present, or clears a stale hint. Every value is a real NCC score on this
// frame; the diagnostics path (scoreOne) never touches hints.
func (c *Catalog) scoreHinted(img *image.RGBA, gray *image.Gray, t preparedTemplate) Hit {
	if t.gray == nil {
		return Hit{ID: t.spec.ID, Scene: t.spec.Scene}
	}
	s, err := c.matchIn(img, gray, t.roi, t)
	if err != nil {
		if !t.windowed {
			c.setHint(t.index, image.Point{}, false)
		}
		return Hit{ID: t.spec.ID, Scene: t.spec.Scene}
	}
	if s.Value >= t.spec.Threshold {
		c.setHint(t.index, s.Peak, true)
	} else if !t.windowed {
		c.setHint(t.index, image.Point{}, false) // Only a full-ROI miss forgets the peak.
	}
	return Hit{ID: t.spec.ID, Scene: t.spec.Scene, Value: s.Value}
}

// scoreOne is the exhaustive form used by diagnostics; it never reads hints.
func (c *Catalog) scoreOne(img *image.RGBA, gray *image.Gray, t preparedTemplate) Hit {
	if t.gray == nil {
		return Hit{ID: t.spec.ID, Scene: t.spec.Scene}
	}
	s, err := c.matchIn(img, gray, t.roi, t)
	if err != nil {
		return Hit{ID: t.spec.ID, Scene: t.spec.Scene}
	}
	return Hit{ID: t.spec.ID, Scene: t.spec.Scene, Value: s.Value}
}

func (c *Catalog) matchIn(img *image.RGBA, gray *image.Gray, roi image.Rectangle, t preparedTemplate) (match.Score, error) {
	return c.matcher.Match(match.Query{
		Image:    img,
		Gray:     gray,
		ROI:      roi,
		Template: t.gray,
		Mask:     t.mask,
		Prepared: t.ncc,
	})
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

func lumaBlank(img *image.RGBA) (bool, engine.GateKind) {
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return true, engine.GateUncertain
	}
	var sum, n float64
	low, high := 255.0, 0.0
	for y := b.Min.Y; y < b.Max.Y; y += 8 {
		for x := b.Min.X; x < b.Max.X; x += 8 {
			c := img.RGBAAt(x, y)
			value := 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
			sum += value
			low, high = min(low, value), max(high, value)
			n++
		}
	}
	if n == 0 {
		return true, engine.GateUncertain
	}
	avg := sum / n
	// A dark/white stage or full-screen effect can still leave readable HUD
	// landmarks. Mean brightness alone is not evidence of a blank capture.
	if (avg < 25 || avg > 235) && high-low <= 12 {
		return true, engine.GateBlank
	}
	return false, 0
}

// ColorGate 把旧的 ClassifyScreen 包成 Gate，供无模板时回退。
type ColorGate struct {
	Mode detect.ContentMode
}

func (g ColorGate) Decide(img *image.RGBA) engine.GateDecision {
	if img == nil {
		return engine.GateDecision{Kind: engine.GateUncertain}
	}
	switch detect.ClassifyScreen(img, g.Mode) {
	case detect.ScreenFighting:
		return engine.GateDecision{Kind: engine.GateFight, SceneID: "color-fight", Confidence: 1}
	case detect.ScreenBlank:
		return engine.GateDecision{Kind: engine.GateBlank, SceneID: "blank", Confidence: 1}
	case detect.ScreenUnknown:
		return engine.GateDecision{Kind: engine.GateUncertain, SceneID: "unknown"}
	default:
		return engine.GateDecision{Kind: engine.GateNotFight, SceneID: "color-other", Confidence: 1}
	}
}

// NewGate 优先用模板目录；没有可用模板则回退颜色密度。
func NewGate(cfg config.Config) (engine.Gate, string, error) {
	cat, err := Load(cfg)
	if err != nil {
		return nil, "", err
	}
	if cat != nil {
		return cat, fmt.Sprintf("templates:%d", len(cat.templates)), nil
	}
	return ColorGate{Mode: parseMode(cfg.Layout.ContentMode)}, "color-fallback", nil
}
