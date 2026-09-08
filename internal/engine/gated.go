package engine

import (
	"crypto/sha256"
	"image"
	"sync"
	"time"
)

// GateKind 是场景门闩的四种结论。场景名本身来自资源，不写死在代码里。
type GateKind int

const (
	GateFight GateKind = iota
	GateNotFight
	GateUncertain
	GateBlank
)

// GateDecision 是门闩对一帧画面的判断。
type GateDecision struct {
	Kind          GateKind
	SceneID       string
	Confidence    float64
	LayoutProfile string
}

// Gate 只负责「这帧是不是对局」，不采豆。
type Gate interface {
	Decide(img *image.RGBA) GateDecision
}

type FightSupportGate interface {
	SupportsFight(img *image.RGBA, profile string) bool
}

// Identity 是 VS / 换人画面上认出的双方账号。
type Identity struct {
	Side string
	Mine string
	Opp  string
}

// SideGuesser 在 VS / 换人画面上认人。
type SideGuesser func(img *image.RGBA, scene string) Identity

// Gated 先过门闩，再把对局帧交给内层引擎采豆。
type Gated struct {
	Gate           Gate
	Inner          Engine
	Guess          SideGuesser
	name           string
	mu             sync.Mutex // Prefer and Analyze must belong to the same frame.
	lastFight      GateDecision
	lastAt         time.Time
	lastBounds     image.Rectangle
	pendingProfile string
	pendingAt      time.Time
	pendingImage   [32]byte
}

// NewGated 包装任意 Engine。gate 或 inner 为 nil 时退回 inner / 空结果。
func NewGated(gate Gate, inner Engine) Engine {
	return NewGatedWithGuess(gate, inner, nil)
}

func NewGatedWithGuess(gate Gate, inner Engine, guess SideGuesser) Engine {
	if inner == nil {
		return nopEngine{}
	}
	if gate == nil {
		return inner
	}
	name := inner.Analyze(nil).Name
	if name == "" {
		name = "engine"
	}
	return &Gated{Gate: gate, Inner: inner, Guess: guess, name: name}
}

type nopEngine struct{}

func (nopEngine) Analyze(*image.RGBA) Result { return Result{Name: "nop"} }

func (g *Gated) Analyze(img *image.RGBA) Result {
	return g.AnalyzeAt(img, time.Now())
}

const profileSwitchConfirmation = 300 * time.Millisecond
const profileEvidenceMaxGap = 300 * time.Millisecond

func (g *Gated) AnalyzeAt(img *image.RGBA, at time.Time) Result {
	g.mu.Lock()
	defer g.mu.Unlock()
	if at.IsZero() {
		at = time.Now()
	}
	if img == nil {
		g.pendingProfile = "" // An explicit missing frame breaks confirmation.
		return Result{Name: g.name + "+gate", Uncertain: true}
	}
	if img.Bounds() != g.lastBounds || (!g.lastAt.IsZero() && at.Before(g.lastAt)) {
		g.clearScene()
		g.lastBounds = img.Bounds()
	}
	// Capture failures can bypass the engine altogether. Preserve the last
	// established scene, but never count missing time as candidate persistence.
	if !g.lastAt.IsZero() && (!at.After(g.lastAt) || at.Sub(g.lastAt) > profileEvidenceMaxGap) {
		g.pendingProfile = ""
	}
	g.lastAt = at
	d := g.Gate.Decide(img)
	var sampled *Result
	// Compare a competing label against fresh evidence in the established
	// coordinate system BEFORE changing it. Animated backgrounds may match a
	// different label twice; that is not evidence that a readable HUD moved.
	if d.Kind == GateFight && g.lastFight.SceneID != "" && d.LayoutProfile != g.lastFight.LayoutProfile {
		r := g.analyzeInner(img, at)
		sampled = &r
		if g.supportsCurrent(img, r) {
			d = g.lastFight
			d.Confidence = 0 // Continuation, not a fresh primary-template score.
		}
	}
	// A single competing marker cannot switch coordinate profiles and erase
	// active clocks. Require a second independent image of the new profile.
	if d.Kind == GateFight && g.lastFight.SceneID != "" && d.LayoutProfile != g.lastFight.LayoutProfile {
		sum := sha256.Sum256(img.Pix)
		if g.pendingProfile != d.LayoutProfile {
			g.pendingProfile, g.pendingAt, g.pendingImage = d.LayoutProfile, at, sum
			d = GateDecision{Kind: GateUncertain}
		} else if sum == g.pendingImage || at.Sub(g.pendingAt) < profileSwitchConfirmation {
			d = GateDecision{Kind: GateUncertain}
		} else {
			g.pendingProfile = ""
		}
	} else {
		g.pendingProfile = ""
	}
	// Explicit ordinary pages, including VS and result, take priority over any
	// old battle context. They must never be hidden by a sticky fight state.
	if d.Kind == GateNotFight || (d.Kind != GateFight && d.SceneID != "" && d.SceneID != "blank") {
		g.clearScene()
	}
	id := Identity{}
	if g.Guess != nil && img != nil {
		id = g.Guess(img, d.SceneID)
	}

	if p, ok := g.Inner.(LayoutPreferrer); ok {
		if d.Kind == GateNotFight {
			p.Prefer("")
		} else if d.Kind == GateFight && (d.LayoutProfile == "camp" || d.LayoutProfile == "duel") {
			if sampled != nil && sampled.LayoutProfile != d.LayoutProfile {
				sampled = nil // A confirmed transition needs the new coordinates.
			}
			p.Prefer(d.LayoutProfile)
		}
	}

	// 大厅/结算：明确不在对局，不采豆。
	if d.Kind == GateNotFight {
		return Result{
			Name:       g.name + "+gate",
			Fighting:   false,
			Scene:      d.SceneID,
			GateScore:  d.Confidence,
			PlayerSide: id.Side,
			PlayerName: id.Mine,
			OppName:    id.Opp,
		}
	}

	// 对局、换人、看不清：都采豆。模板偶发认丢不能把豆清空。
	var res Result
	if sampled != nil {
		res = *sampled
	} else {
		res = g.analyzeInner(img, at)
	}
	if res.Name == "" {
		res.Name = g.name
	}
	res.Name += "+gate"
	res.Scene = d.SceneID
	res.GateScore = d.Confidence
	res.PlayerSide = id.Side
	res.PlayerName = id.Mine
	res.OppName = id.Opp
	if d.Kind == GateFight {
		g.lastFight = d
		res.Fighting = true
		if len(res.Beads) == 0 {
			res.Uncertain = true
		}
		return res
	}
	if (d.SceneID == "" || d.SceneID == "blank") && g.lastFight.SceneID != "" {
		// Require current evidence in three separate areas: a battle control
		// and readable calibrated beads at BOTH corners. Never reuse old beads.
		if d.Kind != GateBlank && g.supportsCurrent(img, res) {
			res.Scene, res.Fighting = g.lastFight.SceneID, true
			return res
		}
		// Retain context, NEVER permission to count. An occlusion is not a
		// scene transition, regardless of duration. Only current bilateral HUD
		// + control can resume observation; explicit pages/geometry clear it.
		res.Scene = g.lastFight.SceneID
	}
	if d.SceneID != "" {
		// VS / 换人 / 选人：场景认出来了，冻结，不开钟。
		res.Fighting = false
		res.Uncertain = true
		return res
	}
	// No scene evidence: colored pixels cannot prove that a match is in progress.
	res.Fighting = false
	res.Uncertain = true
	return res
}

func (g *Gated) clearScene() {
	g.lastFight = GateDecision{}
	g.pendingProfile = ""
}

func (g *Gated) analyzeInner(img *image.RGBA, at time.Time) Result {
	if timed, ok := g.Inner.(TimedEngine); ok {
		return timed.AnalyzeAt(img, at)
	}
	return g.Inner.Analyze(img)
}

func (g *Gated) supportsCurrent(img *image.RGBA, res Result) bool {
	if res.Uncertain || !hasBilateralHUD(res.Beads) || res.LayoutProfile != g.lastFight.LayoutProfile {
		return false
	}
	support, ok := g.Gate.(FightSupportGate)
	return ok && support.SupportsFight(img, g.lastFight.LayoutProfile)
}

func hasBilateralHUD(beads []BeadInfo) bool {
	var known [2]int
	seen := map[string]bool{}
	for _, b := range beads {
		if b.Unknown || b.Conf < .6 || len(b.Label) < 2 || seen[b.Label] {
			continue
		}
		seen[b.Label] = true
		if b.Label[0] == 'L' {
			known[0]++
		}
		if b.Label[0] == 'R' {
			known[1]++
		}
	}
	return known[0] >= 3 && known[1] >= 3
}

func readyBeads(beads []BeadInfo) int {
	n := 0
	for _, b := range beads {
		if b.Lit && !b.Unknown {
			n++
		}
	}
	return n
}
