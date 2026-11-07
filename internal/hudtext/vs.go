package hudtext

import (
	"image"
	"math"
	"strings"

	"narutotimer/internal/detect"
)

// Both the three-ninja opening and the one-ninja death swap put the account
// on this footer line. Exclude the avatar/loading percentage, rank and title.
// ponytail: these boxes cover the current VS footer; add a layout only if the game moves it.
func vsNameRegions(area detect.ContentArea, bounds image.Rectangle) ([2]image.Rectangle, bool) {
	var out [2]image.Rectangle
	scale := area.HUDScale(960, 540)
	for i, r := range [2][4]float64{{.11, .905, .08, .05}, {.828, .905, .085, .05}} {
		side := "left"
		if i == 1 {
			side = "right"
		}
		x0 := area.MapHUDNormalized(r[0], 0, side, 960, 540).X
		x1 := area.MapHUDNormalized(r[0]+r[2], 0, side, 960, 540).X
		bottom := area.Y + area.H
		y0 := bottom - int(math.Round((1-r[1])*540*scale))
		y1 := bottom - int(math.Round((1-r[1]-r[3])*540*scale))
		out[i] = image.Rect(x0, y0, x1, y1).Intersect(bounds)
		if out[i].Dx() < 16 || out[i].Dy() < 8 {
			return out, false
		}
	}
	return out, true
}

// A footer has no account parentheses. Only a complete configured name in two
// views is evidence: never a substring, a rank/percentage, or an unknown word
// interpreted as the opponent (a decorative title could otherwise flip sides).
func footerAccount(raw [variants]string, names []string) string {
	votes := map[string]int{}
	for _, text := range raw {
		text = strings.Join(strings.Fields(text), "")
		for _, name := range names {
			if text != "" && text == name {
				votes[text]++
				if votes[text] >= 2 {
					return text
				}
				break
			}
		}
	}
	return ""
}
