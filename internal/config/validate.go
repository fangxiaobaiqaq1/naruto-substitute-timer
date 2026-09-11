package config

import (
	"fmt"
	"math"
)

func Validate(c Config) error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schemaVersion: got %d, want %d", c.SchemaVersion, SchemaVersion)
	}
	if len(c.Device.ProcessNames) == 0 && len(c.Device.TitlePatterns) == 0 {
		return fmt.Errorf("device: processNames and titlePatterns cannot both be empty")
	}
	if c.Capture.TimeoutMS <= 0 {
		return fmt.Errorf("capture.timeoutMs: must be positive")
	}
	if c.Capture.MuMu.Selection != "" && c.Capture.MuMu.Selection != "auto" && c.Capture.MuMu.Selection != "manual" {
		return fmt.Errorf("capture.mumu.selection: choose auto or manual")
	}
	if c.Capture.MuMu.Instance < 0 || c.Capture.MuMu.DisplayID < 0 {
		return fmt.Errorf("capture.mumu: instance and displayId must be nonnegative")
	}
	for _, method := range c.Capture.PreferredMethods {
		if method != "mumu-sdk" && method != "printwindow-fullcontent" {
			return fmt.Errorf("capture.preferredMethods: unsupported method %q", method)
		}
	}
	if err := validateRect("capture.hudRegion", c.Capture.HUDRegion); err != nil {
		return err
	}
	if c.Layout.ReferenceWidth <= 0 || c.Layout.ReferenceHeight <= 0 {
		return fmt.Errorf("layout: reference dimensions must be positive")
	}
	if c.Layout.ContentMode != "auto" && c.Layout.ContentMode != "stretch" && c.Layout.ContentMode != "letterbox" {
		return fmt.Errorf("layout.contentMode: unsupported value %q", c.Layout.ContentMode)
	}
	if !between(c.Layout.AutoAspectTolerance, 0, 0.25) {
		return fmt.Errorf("layout.autoAspectTolerance: must be in (0, 0.25]")
	}
	if c.Layout.BeadsPerSide != 4 && c.Layout.BeadsPerSide != 6 {
		return fmt.Errorf("layout.beadsPerSide: must be 4 or explicitly calibrated 6")
	}
	if p := c.Layout.PreferredProfile; p != "" && p != "auto" && p != "camp" && p != "duel" {
		return fmt.Errorf("layout.preferredProfile: must be auto, camp or duel")
	}
	if _, explicit := c.Layout.Profiles["camp"]; !explicit {
		if len(c.Layout.Left.NominalCenters) != c.Layout.BeadsPerSide {
			return fmt.Errorf("layout.left.nominalCenters: got %d, want %d", len(c.Layout.Left.NominalCenters), c.Layout.BeadsPerSide)
		}
		if len(c.Layout.Right.NominalCenters) != c.Layout.BeadsPerSide {
			return fmt.Errorf("layout.right.nominalCenters: got %d, want %d", len(c.Layout.Right.NominalCenters), c.Layout.BeadsPerSide)
		}
		if err := validateRect("layout.left.search", c.Layout.Left.Search); err != nil {
			return err
		}
		if err := validateRect("layout.right.search", c.Layout.Right.Search); err != nil {
			return err
		}
		for side, points := range map[string][]NormalizedPoint{"left": c.Layout.Left.NominalCenters, "right": c.Layout.Right.NominalCenters} {
			for i, p := range points {
				if !unit(p.X) || !unit(p.Y) {
					return fmt.Errorf("layout.%s.nominalCenters[%d]: coordinates must be in [0,1]", side, i)
				}
			}
		}
	}
	for name := range c.Layout.Profiles {
		if name != "camp" && name != "duel" {
			return fmt.Errorf("layout.profiles: unsupported profile %q", name)
		}
	}
	for _, name := range []string{"camp", "duel"} {
		p := c.Layout.Profile(name)
		for side, layout := range map[string]SideLayout{"left": p.Left, "right": p.Right} {
			path := "layout.profiles." + name + "." + side
			if len(layout.NominalCenters) != c.Layout.BeadsPerSide {
				return fmt.Errorf("%s.nominalCenters: got %d, want %d; six slots require explicit calibration", path, len(layout.NominalCenters), c.Layout.BeadsPerSide)
			}
			if err := validateRect(path+".search", layout.Search); err != nil {
				return err
			}
			for i, point := range layout.NominalCenters {
				if !unit(point.X) || !unit(point.Y) {
					return fmt.Errorf("%s.nominalCenters[%d]: coordinates must be in [0,1]", path, i)
				}
			}
		}
	}
	if c.Layout.RelocalizeAfterLowConfidenceFrames <= 0 {
		return fmt.Errorf("layout.relocalizeAfterLowConfidenceFrames: must be positive")
	}
	if len(c.Vision.Dark) == 0 || len(c.Vision.Light) == 0 || len(c.Vision.Gold) == 0 {
		return fmt.Errorf("vision: dark, light and gold ranges cannot be empty")
	}
	for name, ranges := range map[string][]HSVRange{"dark": c.Vision.Dark, "light": c.Vision.Light, "gold": c.Vision.Gold} {
		for i, r := range ranges {
			if err := validateHSV(fmt.Sprintf("vision.%s[%d]", name, i), r); err != nil {
				return err
			}
		}
	}
	weightSum := c.Vision.ColorWeight + c.Vision.ShapeWeight + c.Vision.PositionWeight + c.Vision.SymmetryWeight
	if math.Abs(weightSum-1) > 1e-9 {
		return fmt.Errorf("vision weights: sum is %.6f, want 1", weightSum)
	}
	for name, value := range map[string]float64{
		"vision.unknownBelow":               c.Vision.UnknownBelow,
		"vision.minimumMargin":              c.Vision.MinimumMargin,
		"vision.screenFightThreshold":       c.Vision.ScreenFightThreshold,
		"sequence.minimumDecodedConfidence": c.Sequence.MinimumDecodedConfidence,
	} {
		if !unit(value) {
			return fmt.Errorf("%s: must be in [0,1]", name)
		}
	}
	if len(c.Sequence.Allowed) == 0 {
		return fmt.Errorf("sequence.allowed: cannot be empty")
	}
	for i, states := range c.Sequence.Allowed {
		if len(states) != c.Layout.BeadsPerSide {
			return fmt.Errorf("sequence.allowed[%d]: got %d states, want %d", i, len(states), c.Layout.BeadsPerSide)
		}
	}
	if err := validateScene(c.Scene); err != nil {
		return err
	}
	if c.Tracking.PollIntervalMS <= 0 || c.Tracking.WindowFrames <= 0 || c.Tracking.MinimumConfirmFrames <= 0 {
		return fmt.Errorf("tracking: intervals and frame counts must be positive")
	}
	if c.Tracking.MinimumConfirmFrames > c.Tracking.WindowFrames {
		return fmt.Errorf("tracking.minimumConfirmFrames: cannot exceed windowFrames")
	}
	if c.Debug.Enabled && c.Debug.Directory == "" {
		return fmt.Errorf("debug.directory: required when debug is enabled")
	}
	if c.Debug.RetentionFiles < 0 || c.Debug.MinimumIntervalMS < 0 {
		return fmt.Errorf("debug: retentionFiles and minimumIntervalMs cannot be negative")
	}
	if c.UI.PollIntervalMS <= 0 || c.UI.IdlePollIntervalMS <= 0 {
		return fmt.Errorf("ui: pollIntervalMs and idlePollIntervalMs must be positive")
	}
	if c.UI.WindowWidth <= 0 || c.UI.WindowHeight <= 0 || c.UI.MiniWidth <= 0 || c.UI.MiniHeight <= 0 {
		return fmt.Errorf("ui: window and mini dimensions must be positive")
	}
	if c.UI.SubstituteCooldownSeconds <= 0 {
		return fmt.Errorf("ui.substituteCooldownSeconds: must be positive")
	}
	if c.UI.WindowOpacity < 0.40 || c.UI.WindowOpacity > 1.00 {
		return fmt.Errorf("ui.windowOpacity: must be in [0.40,1.00]")
	}
	if c.UI.FontScale < 0.80 || c.UI.FontScale > 1.60 {
		return fmt.Errorf("ui.fontScale: must be in [0.80,1.60]")
	}
	switch c.UI.PlayerSide {
	case "ask", "left", "right", "auto":
	default:
		return fmt.Errorf("ui.playerSide: must be ask, left, right or auto")
	}
	return nil
}

func validateScene(s SceneConfig) error {
	if !s.Enabled {
		return nil
	}
	if len(s.FightScenes) == 0 {
		return fmt.Errorf("scene.fightScenes: required when scene is enabled")
	}
	if len(s.EndScenes) == 0 {
		return fmt.Errorf("scene.endScenes: required when scene is enabled")
	}
	for name, value := range map[string]float64{
		"scene.minFightScore":  s.MinFightScore,
		"scene.minOtherScore":  s.MinOtherScore,
		"scene.margin":         s.Margin,
		"scene.uncertainBelow": s.UncertainBelow,
	} {
		if !unit(value) {
			return fmt.Errorf("%s: must be in [0,1]", name)
		}
	}
	return nil
}

func validateRect(path string, r NormalizedRect) error {
	if !unit(r.X) || !unit(r.Y) || r.Width <= 0 || r.Height <= 0 || r.X+r.Width > 1 || r.Y+r.Height > 1 {
		return fmt.Errorf("%s: rectangle must be positive and contained in [0,1]", path)
	}
	return nil
}

func validateHSV(path string, r HSVRange) error {
	values := []float64{r.HMin, r.HMax, r.SMin, r.SMax, r.VMin, r.VMax}
	for _, v := range values {
		if !unit(v) {
			return fmt.Errorf("%s: HSV values must be in [0,1]", path)
		}
	}
	if r.HMin > r.HMax || r.SMin > r.SMax || r.VMin > r.VMax {
		return fmt.Errorf("%s: min values cannot exceed max values", path)
	}
	return nil
}

func unit(v float64) bool              { return v >= 0 && v <= 1 }
func between(v, min, max float64) bool { return v > min && v <= max }
