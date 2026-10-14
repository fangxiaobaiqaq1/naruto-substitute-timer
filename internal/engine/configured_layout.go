package engine

import (
	"image"
	"slices"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
)

// ConfiguredLayout is immutable after construction and shared by both engines.
type ConfiguredLayout struct {
	mode       detect.ContentMode
	preferred  string
	profiles   map[string]config.LayoutProfile
	count      int
	reference  image.Point
	tolerance  float64
	autoAnchor bool
}

func NewConfiguredLayout(mode detect.ContentMode, cfg config.LayoutConfig) *ConfiguredLayout {
	defaults := config.Default().Layout
	autoAnchor := len(cfg.Profiles) == 0 && cfg.BeadsPerSide == defaults.BeadsPerSide &&
		slices.Equal(cfg.Left.NominalCenters, defaults.Left.NominalCenters) &&
		slices.Equal(cfg.Right.NominalCenters, defaults.Right.NominalCenters)
	return &ConfiguredLayout{mode: mode, preferred: cfg.PreferredProfile, count: cfg.BeadsPerSide, autoAnchor: autoAnchor,
		reference: image.Pt(cfg.ReferenceWidth, cfg.ReferenceHeight), tolerance: cfg.AutoAspectTolerance,
		profiles: map[string]config.LayoutProfile{"camp": cloneProfile(cfg.Profile("camp")), "duel": cloneProfile(cfg.Profile("duel"))}}
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
	return l.PositionsIn(name, detect.ComputeContentArea(w, h, l.mode))
}

// ContentArea is shared with scene matching. A size alone cannot prove bars.
func (l *ConfiguredLayout) ContentArea(img *image.RGBA) (detect.ContentArea, bool) {
	return detect.ResolveContentArea(img, l.mode, l.reference.X, l.reference.Y, l.tolerance)
}

func (l *ConfiguredLayout) ReferenceSize() image.Point { return l.reference }

// PositionsInImage aligns built-in rows with a current wide-screen HUD anchor.
// Explicit user calibrations remain authoritative. Size-only callers never
// invent safe-area margins from a resolution or from old frames.
func (l *ConfiguredLayout) PositionsInImage(name string, img *image.RGBA, ca detect.ContentArea) []detect.BeadPosition {
	positions := l.PositionsIn(name, ca)
	if !l.autoAnchor {
		return positions
	}
	for _, first := range positions {
		if first.Idx != 0 {
			continue
		}
		dx := detect.HUDRowOffset(img, ca, image.Pt(first.X, first.Y), first.Side == "right", name)
		for i := range positions {
			if positions[i].Side == first.Side {
				positions[i].X += dx
			}
		}
	}
	return positions
}

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
