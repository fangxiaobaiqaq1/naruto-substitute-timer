package engine

import (
	"image"
	"math"
	"slices"
	"sync/atomic"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
)

// ConfiguredLayout is shared by both engines. Only the player's region
// calibration may be replaced after construction (SetCalibration).
type ConfiguredLayout struct {
	mode        detect.ContentMode
	preferred   string
	profiles    map[string]config.LayoutProfile
	count       int
	reference   image.Point
	tolerance   float64
	autoAnchor  bool
	calibration atomic.Pointer[calibrationState]
	revisions   atomic.Uint64
}

// calibrationState is one immutable region calibration (docs/区域校准.md).
type calibrationState struct {
	revision uint64
	profile  string // "" = automatic
	beads    [2]*config.NormalizedRect
	avatars  [2]*config.NormalizedRect
}

func newCalibrationState(c *config.RegionCalibration, revision uint64) *calibrationState {
	s := &calibrationState{revision: revision}
	if c == nil || (c.Profile != "camp" && c.Profile != "duel") {
		return s
	}
	s.profile = c.Profile
	for i, r := range [4]*config.NormalizedRect{c.LeftBeads, c.RightBeads, c.LeftAvatar, c.RightAvatar} {
		// Same bounds as config validation; anything else stays automatic.
		if r == nil || !(r.X >= 0 && r.Y >= 0 && r.Width > 0 && r.Height > 0 && r.X+r.Width <= 1 && r.Y+r.Height <= 1) {
			continue
		}
		v := *r
		if i < 2 {
			s.beads[i] = &v
		} else {
			s.avatars[i-2] = &v
		}
	}
	return s
}

func NewConfiguredLayout(mode detect.ContentMode, cfg config.LayoutConfig) *ConfiguredLayout {
	defaults := config.Default().Layout
	autoAnchor := len(cfg.Profiles) == 0 && cfg.BeadsPerSide == defaults.BeadsPerSide &&
		slices.Equal(cfg.Left.NominalCenters, defaults.Left.NominalCenters) &&
		slices.Equal(cfg.Right.NominalCenters, defaults.Right.NominalCenters)
	l := &ConfiguredLayout{mode: mode, preferred: cfg.PreferredProfile, count: cfg.BeadsPerSide, autoAnchor: autoAnchor,
		reference: image.Pt(cfg.ReferenceWidth, cfg.ReferenceHeight), tolerance: cfg.AutoAspectTolerance,
		profiles: map[string]config.LayoutProfile{"camp": cloneProfile(cfg.Profile("camp")), "duel": cloneProfile(cfg.Profile("duel"))}}
	l.calibration.Store(newCalibrationState(cfg.Calibration, 0))
	return l
}

// SetCalibration replaces the player's region calibration (nil = automatic)
// while frames are being analysed; the next frame uses it.
func (l *ConfiguredLayout) SetCalibration(c *config.RegionCalibration) {
	l.calibration.Store(newCalibrationState(c, l.revisions.Add(1)))
}

// CalibrationRevision changes with every SetCalibration (0 = never replaced).
func (l *ConfiguredLayout) CalibrationRevision() uint64 { return l.calibration.Load().revision }

// Pinned is this layout fixed to its current calibration, so one frame never
// mixes two calibrations.
func (l *ConfiguredLayout) Pinned() *ConfiguredLayout {
	p := &ConfiguredLayout{mode: l.mode, preferred: l.preferred, profiles: l.profiles, count: l.count,
		reference: l.reference, tolerance: l.tolerance, autoAnchor: l.autoAnchor}
	p.calibration.Store(l.calibration.Load())
	return p
}

func cloneProfile(p config.LayoutProfile) config.LayoutProfile {
	p.Left.NominalCenters = append([]config.NormalizedPoint(nil), p.Left.NominalCenters...)
	p.Right.NominalCenters = append([]config.NormalizedPoint(nil), p.Right.NominalCenters...)
	return p
}

func (l *ConfiguredLayout) Mode() detect.ContentMode { return l.mode }
func (l *ConfiguredLayout) PreferredProfile() string { return l.preferred }
func (l *ConfiguredLayout) Positions(w, h int) []detect.BeadPosition {
	name := l.preferred
	if name != "duel" {
		name = "camp"
	}
	return l.PositionsFor(name, w, h)
}
func (l *ConfiguredLayout) PositionsFor(name string, w, h int) []detect.BeadPosition {
	ca := detect.ComputeContentArea(w, h, l.mode)
	positions := l.PositionsIn(name, ca)
	calibrate(positions, l.sideCalibrations(name, image.Rect(0, 0, w, h), ca))
	return positions
}

// ContentArea is shared with scene matching. A size alone cannot prove bars.
func (l *ConfiguredLayout) ContentArea(img *image.RGBA) (detect.ContentArea, bool) {
	return detect.ResolveContentArea(img, l.mode, l.reference.X, l.reference.Y, l.tolerance)
}

func (l *ConfiguredLayout) ReferenceSize() image.Point { return l.reference }

// PositionsInImage aligns built-in rows with a current wide-screen HUD anchor.
// Explicit user calibrations remain authoritative. Size-only callers never
// invent safe-area margins from a resolution or from old frames. Sides with
// a player-drawn bead box use that box instead (docs/区域校准.md).
func (l *ConfiguredLayout) PositionsInImage(name string, img *image.RGBA, ca detect.ContentArea) []detect.BeadPosition {
	positions := l.PositionsIn(name, ca)
	var cals [2]*detect.SideCalibration
	if img != nil {
		cals = l.sideCalibrations(name, img.Bounds(), ca)
	}
	if l.autoAnchor {
		for _, first := range positions {
			if first.Idx != 0 || cals[sideIndex(first.Side)].Beads() {
				continue
			}
			dx := detect.HUDRowOffset(img, ca, image.Pt(first.X, first.Y), first.Side == "right", name)
			for i := range positions {
				if positions[i].Side == first.Side {
					positions[i].X += dx
				}
			}
		}
	}
	calibrate(positions, cals)
	return positions
}

func sideIndex(side string) int {
	if side == "right" {
		return 1
	}
	return 0
}

// calibrate moves calibrated rows onto their transform and tags every
// position of a calibrated side with its geometry.
func calibrate(positions []detect.BeadPosition, cals [2]*detect.SideCalibration) {
	for i := range positions {
		c := cals[sideIndex(positions[i].Side)]
		if c == nil {
			continue
		}
		positions[i].Cal = c
		if c.Beads() {
			p := c.Map(positions[i].LX, positions[i].LY)
			positions[i].X, positions[i].Y = p.X, p.Y
		}
	}
}

// sideCalibrations is each side's calibrated geometry for one frame and
// profile (nil = automatic). Boxes are fractions of the whole frame. A bead
// box defines a similarity transform from the calibration profile's nominal
// centres; the same transform maps the other profile. A portrait box drawn on
// the other profile moves with that side's bead 0.
func (l *ConfiguredLayout) sideCalibrations(name string, frame image.Rectangle, ca detect.ContentArea) [2]*detect.SideCalibration {
	var out [2]*detect.SideCalibration
	s := l.calibration.Load()
	drawn, ok := l.profiles[s.profile]
	current, okCurrent := l.profiles[name]
	if !ok || !okCurrent || frame.Empty() {
		return out
	}
	w, h := float64(frame.Dx()), float64(frame.Dy())
	for i, sides := range [2][2]config.SideLayout{{drawn.Left, current.Left}, {drawn.Right, current.Right}} {
		ref, now := sides[0].NominalCenters, sides[1].NominalCenters
		if len(ref) < 4 || len(now) < 1 {
			continue
		}
		c := &detect.SideCalibration{RefX: ref[0].X * detect.HUDReferenceWidth, RefY: ref[0].Y * detect.HUDReferenceHeight}
		if r := s.beads[i]; r != nil {
			// Bead 0 is the outermost of four evenly spaced centres in the box.
			if pitch := math.Abs(ref[3].X-ref[0].X) * detect.HUDReferenceWidth / 3; pitch > 0 {
				x := r.X + .5*r.Width/4
				if i == 1 {
					x = r.X + r.Width - .5*r.Width/4
				}
				c.X0 = float64(frame.Min.X) + x*w
				c.Y0 = float64(frame.Min.Y) + (r.Y+r.Height/2)*h
				c.Scale = r.Width * w / 4 / pitch
			}
		}
		if r := s.avatars[i]; r != nil {
			c.Avatar = detect.PixelBox{X: float64(frame.Min.X) + r.X*w, Y: float64(frame.Min.Y) + r.Y*h, W: r.Width * w, H: r.Height * h}
			if name != s.profile {
				scale := c.Scale
				if scale == 0 {
					scale = ca.HUDScale(detect.HUDReferenceWidth, detect.HUDReferenceHeight)
				}
				c.Avatar.X += scale * (now[0].X*detect.HUDReferenceWidth - c.RefX)
				c.Avatar.Y += scale * (now[0].Y*detect.HUDReferenceHeight - c.RefY)
			}
		}
		if c.Beads() || c.Portrait() {
			out[i] = c
		}
	}
	return out
}

// PositionsIn is the automatic mapping only; calibrated sides need the frame
// (PositionsInImage, PositionsFor).
func (l *ConfiguredLayout) PositionsIn(name string, ca detect.ContentArea) []detect.BeadPosition {
	p, ok := l.profiles[name]
	if !ok || ca.W <= 0 || ca.H <= 0 {
		return nil
	}
	out := make([]detect.BeadPosition, 0, l.count*2)
	for _, side := range []struct {
		name string
		data config.SideLayout
	}{{"left", p.Left}, {"right", p.Right}} {
		// Invalid/missing calibration must not synthesize unverified slots.
		if len(side.data.NominalCenters) != l.count {
			return nil
		}
		for i, point := range side.data.NominalCenters {
			mapped := ca.MapHUDNormalized(point.X, point.Y, side.name, detect.HUDReferenceWidth, detect.HUDReferenceHeight)
			out = append(out, detect.BeadPosition{Bead: detect.Bead{Side: side.name, Idx: i, LX: point.X * detect.HUDReferenceWidth, LY: point.Y * detect.HUDReferenceHeight}, X: mapped.X, Y: mapped.Y})
		}
	}
	return out
}
