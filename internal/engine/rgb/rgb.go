// Package rgb 实现 RGBEngine：基于纯 RGB 区间判色的检测引擎。
// 配置布局使用固定菱形核心，旧校准工具保留兼容采样接口。
package rgb

import (
	"fmt"
	"image"
	"math"
	"sync"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/ninja"
)

// Engine 保存同步保护的布局偏好，可并发调用。
type Engine struct {
	layout      engine.LayoutProvider
	prefer      string
	mu          sync.Mutex
	locked      string
	vision      config.VisionConfig
	names       *ninja.Reader
	nameTracker [2][2]ninja.Tracker
}

func New(layout engine.LayoutProvider) *Engine {
	return NewConfigured(layout, config.Default().Vision)
}

func NewConfigured(layout engine.LayoutProvider, vision config.VisionConfig) *Engine {
	return &Engine{layout: layout, vision: vision, names: ninja.NewReader()}
}

// Prefer receives a template's explicit layout binding, or clears it at an end scene.
func (e *Engine) Prefer(kind string) {
	e.mu.Lock()
	e.prefer = kind
	e.locked = kind
	e.mu.Unlock()
}

// Analyze 实现 engine.Engine：RGB 区间判色 + 递增规则。
// A scene-bound profile samples only its own calibrated slots.
func (e *Engine) Analyze(img *image.RGBA) engine.Result { return e.AnalyzeAt(img, time.Now()) }

// AnalyzeAt uses acquisition time for retry scheduling so live and replay frames
// make the same name decisions, independent of analysis speed.
func (e *Engine) AnalyzeAt(img *image.RGBA, at time.Time) engine.Result {
	if at.IsZero() {
		at = time.Now()
	}
	res := engine.Result{Name: "rgb"}
	if img == nil {
		return res
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	area := detect.ComputeContentArea(w, h, e.layout.Mode())
	var calibrated *engine.ConfiguredLayout
	vision := e.vision
	if p, ok := e.layout.(*engine.ConfiguredLayout); ok {
		calibrated = p
		var supported bool
		area, supported = p.ContentArea(img)
		if !supported {
			res.Uncertain = true
			res.Scene = "unsupported-resolution"
			return res
		}
		ref := p.ReferenceSize()
		vision.SampleWidthReferencePX *= float64(detect.LogicWidth) / float64(ref.X)
		vision.SampleHeightReferencePX *= float64(detect.LogicHeight) / float64(ref.Y)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	prefer := e.prefer
	positions := func(name string) []detect.BeadPosition {
		if calibrated != nil {
			return calibrated.PositionsIn(name, area)
		}
		if p, ok := e.layout.(engine.ProfiledLayout); ok {
			return p.PositionsFor(name, w, h)
		}
		if name == "duel" {
			return detect.Layout(w, h, e.layout.Mode(), detect.DuelBeads())
		}
		return e.layout.Positions(w, h)
	}
	readouts := map[string][2]ninja.Readout{}
	sample := func(name string) []engine.BeadInfo {
		if _, calibrated := e.layout.(engine.ProfiledLayout); calibrated {
			pos, identified := e.specialPositionsForAt(name, img, positions(name), area, at)
			readouts[name] = identified
			return sampleCalibratedSpecial(img, pos, area, vision, identified)
		}
		return sampleLayout(img, positions(name))
	}
	if p, ok := e.layout.(engine.ProfiledLayout); ok {
		if manual := p.PreferredProfile(); manual == "camp" || manual == "duel" {
			prefer = manual
		}
	}
	if prefer == "camp" || prefer == "duel" {
		res.Name = "rgb-" + prefer
		res.LayoutProfile = prefer
		res.Beads = sample(prefer)
		applyNames(&res, readouts[prefer])
		e.locked = prefer
		return res
	}
	camp := sample("camp")
	duel := sample("duel")
	chosen, name := pickLayout(camp, duel)
	chosen, name = e.stickLayout(camp, duel, chosen, name)
	res.Name = "rgb-" + name
	res.LayoutProfile = name
	res.Beads = chosen
	applyNames(&res, readouts[name])
	return res
}

// sampleCalibrated keeps each core inside its calibrated slot. Searching for the
// brightest nearby pixel follows the cyan rim of an EMPTY bead and turns 2 into 4.
// Ambiguous cores stay unknown; sequence constraints never invent missing votes.
func sampleCalibrated(img *image.RGBA, positions []detect.BeadPosition, area detect.ContentArea, cfg config.VisionConfig) []engine.BeadInfo {
	return sampleCalibratedSpecial(img, positions, area, cfg, [2]ninja.Readout{})
}

func sampleCalibratedSpecial(img *image.RGBA, positions []detect.BeadPosition, area detect.ContentArea, cfg config.VisionConfig, identified [2]ninja.Readout) []engine.BeadInfo {
	w := max(2, int(math.Round(cfg.SampleWidthReferencePX*cfg.CoreScale*float64(area.W)/detect.LogicWidth)))
	h := max(2, int(math.Round(cfg.SampleHeightReferencePX*cfg.CoreScale*float64(area.H)/detect.LogicHeight)))
	var out []engine.BeadInfo
	for index, side := range []engine.Side{engine.Left, engine.Right} {
		palette := identified[index].Palette
		var states []detect.BeadState
		start := len(out)
		for _, p := range positions {
			if p.Side != side.String() {
				continue
			}
			if identified[index].Unverified {
				// Preserve the known row shape through a brief name gap, but no
				// cached color/count may vote or appear as a current observation.
				states = append(states, detect.StateUnknown)
				out = append(out, engine.BeadInfo{X: p.X, Y: p.Y, Label: label(side, p.Idx), Unknown: true})
				continue
			}
			redHighlight := palette == ninja.Red && redLowerBody(img, p, w, h)
			purpleHighlight := palette == ninja.Purple && purpleGlintBody(img, p, w, h)
			goldHighlight := (palette == "" || palette == ninja.Warm) && goldGlintBody(img, p, w, h)
			light, dark, gold, paleGold, blue, total := 0, 0, 0, 0, 0, 0
			for dy := -h / 2; dy <= h/2; dy++ {
				for dx := -w / 2; dx <= w/2; dx++ {
					if !detect.InBeadDiamond(0, 0, w, h, dx, dy) {
						continue
					}
					total++
					if !image.Pt(p.X+dx, p.Y+dy).In(img.Bounds()) {
						continue
					}
					c := img.RGBAAt(p.X+dx, p.Y+dy)
					r, g, b := int(c.R), int(c.G), int(c.B)
					if purpleHighlight && r >= 235 && b >= 235 && g >= 210 && r+12 >= g && b+12 >= g {
						light++
						continue
					}
					// The gold idle glint clips the middle to white. Accept that
					// white only with BOTH current gold body lobes and bounded
					// spatial contrast; not a nearby rim, bar, or cached count.
					if goldHighlight && r >= 235 && g >= 210 && r+12 >= g && g >= b {
						light++
						gold++
						continue
					}
					// Naruto's charged red bean can flash yellow/white in its core.
					// This exception needs the exact skin and its own red lower body;
					// a bright rim/white cover on an empty core is not enough.
					if redHighlight && r >= 235 && g >= 190 && r >= g && g >= b {
						light++
						continue
					}
					if st := specialPixel(palette, r, g, b); st != detect.StateUnknown {
						if st == detect.StateLight {
							light++
						} else {
							dark++
						}
						continue
					}
					switch {
					case r >= 190 && g >= 110 && r-b >= 80 && g-b >= 70:
						light++
						gold++
					case r >= 210 && g >= 180 && r-b >= 35 && g-b >= 30:
						paleGold++
					case g >= 120 && b >= 150 && b-r >= 20:
						light++
						blue++
					case detect.DarkRange.Contains(r, g, b) && b-r >= 15 && b-g >= 8:
						dark++
					}
				}
			}
			// A gold sparkle fades towards pale yellow. Count this highlight
			// only with substantial saturated gold in the same calibrated core;
			// white or pale effects alone remain unknown.
			if gold*4 >= total {
				light += paleGold
				gold += paleGold
			}
			conf := float64(max(light, dark)) / float64(total)
			margin := math.Abs(float64(light-dark)) / float64(total)
			st := detect.StateUnknown
			if conf >= max(0.60, cfg.UnknownBelow) && margin >= max(0.15, cfg.MinimumMargin) {
				if light > dark {
					st = detect.StateLight
				} else {
					st = detect.StateDark
				}
			}
			guard := max(3, int(math.Round(cfg.SampleHeightReferencePX*float64(area.H)/detect.LogicHeight*1.2)))
			if st == detect.StateLight && specialWash(img, p, palette, guard) {
				st = detect.StateUnknown
			}
			if st == detect.StateLight && blue > light/2 && !blueBodyVisible(img, p, w, h) {
				st = detect.StateUnknown
			}
			if st == detect.StateLight && gold > light/2 && !goldHighlight && !goldBodyVisible(img, p, w, h) {
				st = detect.StateUnknown
			}
			states = append(states, st)
			out = append(out, engine.BeadInfo{X: p.X, Y: p.Y, Label: label(side, p.Idx), Lit: st == detect.StateLight, Gold: st == detect.StateLight && gold > light/2, Unknown: st == detect.StateUnknown, Conf: conf})
		}
		if !detect.IsPossiblePrefix(states) {
			for i := start; i < len(out); i++ {
				out[i].Lit = false
				out[i].Gold = false
				out[i].Unknown = true
			}
		}
	}
	return out
}

func pickLayout(camp, duel []engine.BeadInfo) ([]engine.BeadInfo, string) {
	if legalSides(duel) > legalSides(camp) || (legalSides(duel) == legalSides(camp) && knownCount(duel) > knownCount(camp)) {
		return duel, "duel"
	}
	return camp, "camp"
}

func (e *Engine) stickLayout(camp, duel []engine.BeadInfo, chosen []engine.BeadInfo, name string) ([]engine.BeadInfo, string) {
	if e.locked == "" {
		if legalSides(chosen) >= 1 {
			e.locked = name
		}
		return chosen, name
	}
	cur := camp
	if e.locked == "duel" {
		cur = duel
	}
	alt, altName := duel, "duel"
	if e.locked == "duel" {
		alt, altName = camp, "camp"
	}
	// 已锁定的布局仍然合法就别换，避免 4/3 来回切把开钟拖成十几秒。
	if legalSides(cur) >= 1 {
		return cur, e.locked
	}
	if legalSides(alt) >= 1 {
		e.locked = altName
		return alt, altName
	}
	return cur, e.locked
}

func sampleLayout(img *image.RGBA, positions []detect.BeadPosition) []engine.BeadInfo {
	var out []engine.BeadInfo
	for _, side := range []engine.Side{engine.Left, engine.Right} {
		sideStr := side.String()
		var pos []detect.BeadPosition
		var states []detect.BeadState
		for _, p := range positions {
			if p.Side != sideStr {
				continue
			}
			st, nx, ny := locateBead(img, p.X, p.Y, p.Idx)
			p.X, p.Y = nx, ny
			pos = append(pos, p)
			states = append(states, st)
		}
		states = detect.ApplyIncrementRule(states)
		for i, p := range pos {
			c := img.RGBAAt(p.X, p.Y)
			gold := states[i] == detect.StateLight && detect.IsGold(int(c.R), int(c.G), int(c.B))
			out = append(out, engine.BeadInfo{
				X:       p.X,
				Y:       p.Y,
				Label:   label(side, p.Idx),
				Lit:     states[i] == detect.StateLight,
				Gold:    gold,
				Unknown: states[i] == detect.StateUnknown || states[i] == detect.StateGone,
				Conf:    1.0,
			})
		}
	}
	return out
}

func legalSides(beads []engine.BeadInfo) int {
	n := 0
	for _, side := range []byte{'L', 'R'} {
		var st []detect.BeadState
		for _, b := range beads {
			if len(b.Label) == 0 || b.Label[0] != side {
				continue
			}
			switch {
			case b.Unknown:
				st = append(st, detect.StateUnknown)
			case b.Lit:
				st = append(st, detect.StateLight)
			default:
				st = append(st, detect.StateDark)
			}
		}
		if detect.IsLegalPrefix(st) {
			n++
		}
	}
	return n
}

func knownCount(beads []engine.BeadInfo) int {
	n := 0
	for _, b := range beads {
		if !b.Unknown {
			n++
		}
	}
	return n
}

// label 生成豆子编号（L1..L6 / R1..R6）。
func label(side engine.Side, idx int) string {
	prefix := "L"
	if side == engine.Right {
		prefix = "R"
	}
	return fmt.Sprintf("%s%d", prefix, idx+1)
}

// locateBead 在标定附近找豆。亮豆中间有一条暗青装饰带，中心常被判暗，
// 所以一旦附近出现亮核就用亮核，不要被中间那条带子带走。
func locateBead(img *image.RGBA, cx, cy, idx int) (detect.BeadState, int, int) {
	// 第 5/6 槽：4 豆 HUD 这里是血条/木纹。只认标定中心，别漂到邻豆上。
	if idx >= 4 {
		return voteBead(img, cx, cy), cx, cy
	}
	bestS, bestN, bx, by := voteBead(img, cx, cy), classifiedAt(img, cx, cy), cx, cy
	lightS, lightN, lx, ly := detect.StateUnknown, 0, cx, cy
	if bestS == detect.StateLight {
		lightS, lightN, lx, ly = bestS, bestN, cx, cy
	}
	for dy := -6; dy <= 8; dy += 2 {
		for dx := -6; dx <= 6; dx += 2 {
			if dx == 0 && dy == 0 {
				continue
			}
			s := voteBead(img, cx+dx, cy+dy)
			n := classifiedAt(img, cx+dx, cy+dy)
			if s == detect.StateLight && n > lightN {
				lightS, lightN, lx, ly = s, n, cx+dx, cy+dy
			}
			if s != detect.StateUnknown && n > bestN {
				bestS, bestN, bx, by = s, n, cx+dx, cy+dy
			}
		}
	}
	if lightS == detect.StateLight && lightN >= 5 {
		return lightS, lx, ly
	}
	return bestS, bx, by
}

func classifiedAt(img *image.RGBA, cx, cy int) int {
	const w, h = detect.BeadW, detect.BeadH
	n := 0
	b := img.Bounds()
	for dy := -h / 2; dy <= h/2; dy++ {
		for dx := -w / 2; dx <= w/2; dx++ {
			if !detect.InBeadDiamond(0, 0, w, h, dx, dy) {
				continue
			}
			x, y := cx+dx, cy+dy
			if x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y {
				continue
			}
			c := img.RGBAAt(x, y)
			if detect.Classify(int(c.R), int(c.G), int(c.B)) != detect.StateUnknown {
				n++
			}
		}
	}
	return n
}

// voteBead 在菱形采样区域内逐像素分类并投票，返回多数状态。
func voteBead(img *image.RGBA, cx, cy int) detect.BeadState {
	const w, h = detect.BeadW, detect.BeadH
	light, dark, other := 0, 0, 0
	b := img.Bounds()
	for dy := -h / 2; dy <= h/2; dy++ {
		for dx := -w / 2; dx <= w/2; dx++ {
			if !detect.InBeadDiamond(0, 0, w, h, dx, dy) {
				continue
			}
			x, y := cx+dx, cy+dy
			if x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y {
				continue
			}
			c := img.RGBAAt(x, y)
			switch detect.Classify(int(c.R), int(c.G), int(c.B)) {
			case detect.StateLight:
				light++
			case detect.StateDark:
				dark++
			default:
				other++
			}
		}
	}
	// 亮豆中部那条暗青装饰带会拉高 dark。附近有成片亮核就当亮。
	switch {
	case light >= 4 && light*2 >= dark && light+dark > other:
		return detect.StateLight
	case light > dark && light > other:
		return detect.StateLight
	case dark > light && dark > other:
		return detect.StateDark
	default:
		return detect.StateUnknown
	}
}
