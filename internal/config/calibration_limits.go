package config

import "fmt"

// Size limits of a region-calibration box (docs/区域校准.md「框的大小限制」).
// A box far from the automatic HUD scale makes the engine scan huge windows
// with huge templates (seconds per frame, hundreds of MiB) or tiny ones, and
// recognises nothing either way. Such a box is not used: the region stays
// automatic. The same rule, with the same arithmetic, is in the Android core
// (C++ nt::config::CalibrationRectIssue) and in Kotlin (CalibrationLimits:
// Settings.calibRect, scheme import, the editor), so every layer accepts
// exactly the same boxes.
//
// The rule only reads the box's own fractions, never the frame size. u and v
// are the box's width and height in units of the automatic box at HUD scale 1
// along each axis: a bead box (4 pitches by 1 pitch, pitch 25 of the 1600x900
// HUD reference) is 1/16 of the frame width on frames up to 16:9 and 1/36 of
// the frame height on frames from 16:9; a portrait box (68.5 of the 960x540
// reference both ways) is 68.5/960 of the width and 68.5/540 of the height.
// The automatic HUD scale is min(W/1600, H/900), so on every frame the
// automatic box has max(u, v) = 1 exactly: k = max(u, v) is how many times
// the automatic box a box is, whatever the frame's size and aspect.
const (
	// CalibrationMaxScale and CalibrationMinScale bound k.
	CalibrationMaxScale = 2.0
	CalibrationMinScale = 0.5
	// CalibrationMinShape bounds min(u, v)/k: neither side may be under a
	// quarter of the other in these units (a sliver is no bead row or
	// portrait). Correct boxes are at least 1/2 on screens from 8:9 to 32:9.
	CalibrationMinShape = 0.25
)

// CalibrationBoxUnits is a box's width u and height v in automatic-box units
// (avatar: a portrait box, else a bead box).
func CalibrationBoxUnits(avatar bool, r NormalizedRect) (u, v float64) {
	if avatar {
		return r.Width * 960 / 68.5, r.Height * 540 / 68.5
	}
	return r.Width * 16, r.Height * 36
}

// CalibrationRectIssue is "" when r is a usable calibration box of its kind,
// else why it is not used, worded for the player. r must already be a box
// inside the frame (validateRect).
func CalibrationRectIssue(avatar bool, r NormalizedRect) string {
	u, v := CalibrationBoxUnits(avatar, r)
	k, short := u, v
	if v > u {
		k, short = v, u
	}
	kind := "豆框"
	if avatar {
		kind = "头像框"
	}
	switch {
	case !(k <= CalibrationMaxScale):
		return fmt.Sprintf("%s太大：是自动框的 %.2f 倍（只用 0.5–2 倍的框）", kind, k)
	case !(k >= CalibrationMinScale):
		return fmt.Sprintf("%s太小：只有自动框的 %.2f 倍（只用 0.5–2 倍的框）", kind, k)
	case !(short >= k*CalibrationMinShape):
		return fmt.Sprintf("%s太扁或太窄：宽高按自动框折算相差 %.1f 倍（最多 4 倍）", kind, k/short)
	}
	return ""
}

// validateCalibrationLimits is Validate's check of one calibration box (name
// is its JSON key: leftBeads, rightBeads, leftAvatar, rightAvatar).
func validateCalibrationLimits(name string, r NormalizedRect) error {
	avatar := name == "leftAvatar" || name == "rightAvatar"
	if issue := CalibrationRectIssue(avatar, r); issue != "" {
		return fmt.Errorf("layout.calibration.%s: %s", name, issue)
	}
	return nil
}
