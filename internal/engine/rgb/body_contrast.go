package rgb

import "narutotimer/internal/detect"

// Inter-slot probes come from calibrated spacing, not a rounded sample-core
// width. At 1600px, half of a 25px pitch is 13px, while 2*roundedCore is 12px.
func beadHalfPitch(positions []detect.BeadPosition, p detect.BeadPosition, fallback int) int {
	pitch := 0
	for _, other := range positions {
		if other.Side != p.Side || other.Idx == p.Idx {
			continue
		}
		dx := other.X - p.X
		if dx < 0 {
			dx = -dx
		}
		if dx > 0 && (pitch == 0 || dx < pitch) {
			pitch = dx
		}
	}
	if pitch == 0 {
		return fallback
	}
	return max(2, (pitch+1)/2)
}

// bodyContrast pools a few adjacent, already color-verified interior rows.
// One anti-aliased edge pixel must not toggle a body proof on/off. BOTH sides
// still need independent contrast in this same spatial window, never time.
type bodyContrast struct {
	rows                     []struct{ left, right int }
	next, count, left, right int
}

func newBodyContrast(rows int) bodyContrast {
	return bodyContrast{rows: make([]struct{ left, right int }, rows)}
}

func (b *bodyContrast) reset() { b.next, b.count, b.left, b.right = 0, 0, 0, 0 }

func (b *bodyContrast) add(left, right, minimum int) bool {
	if b.count == len(b.rows) {
		b.left -= b.rows[b.next].left
		b.right -= b.rows[b.next].right
	} else {
		b.count++
	}
	b.rows[b.next].left, b.rows[b.next].right = left, right
	b.left += left
	b.right += right
	b.next = (b.next + 1) % len(b.rows)
	return b.count == len(b.rows) && b.left >= minimum*b.count && b.right >= minimum*b.count
}
