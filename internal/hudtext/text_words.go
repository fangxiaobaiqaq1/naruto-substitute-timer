package hudtext

import (
	"image"
	"sort"

	"narutotimer/internal/ocr"
)

// OCR may attach a remote scenery mark to a title on the same horizontal
// line. Separate by geometry, not by a ninja-name alias or spelling rewrite.
// Remove only small, detached groups (<1/3 of the main text width) across a
// gap larger than two glyph heights. Substantial separated groups are kept:
// uncertain segmentation must not silently discard part of a name/account.
func wordsForRow(lines []ocr.Line, rect image.Rectangle) []ocr.Word {
	var words []ocr.Word
	for _, line := range lines {
		if len(line.Words) == 0 {
			continue
		}
		valid := true
		top, bottom := line.Words[0].Y, line.Words[0].Y+line.Words[0].Height
		for _, w := range line.Words {
			if !validWordBox(w, rect.Inset(-3)) {
				valid = false
				break
			}
			top = min(top, w.Y)
			bottom = max(bottom, w.Y+w.Height)
		}
		if !valid {
			continue
		}
		part := line.Words
		if len(part) > 1 && part[0].Height < (bottom-top)*.25 {
			part = part[1:]
		}
		words = append(words, part...)
	}
	sort.SliceStable(words, func(i, j int) bool { return words[i].X < words[j].X })
	if len(words) < 2 {
		return words
	}
	type group struct {
		first, last int
		width       float64
	}
	groups := []group{{first: 0}}
	for i, w := range words {
		if i > 0 {
			prev := words[i-1]
			if w.X-(prev.X+prev.Width) > max(12, 2*max(w.Height, prev.Height)) {
				groups[len(groups)-1].last = i
				groups = append(groups, group{first: i})
			}
		}
		groups[len(groups)-1].width += w.Width
	}
	groups[len(groups)-1].last = len(words)
	main := 0
	for i, g := range groups {
		if g.width > groups[main].width {
			main = i
		}
	}
	first, last := 0, len(groups)-1
	for first < main && groups[first].width*3 < groups[main].width {
		first++
	}
	for last > main && groups[last].width*3 < groups[main].width {
		last--
	}
	return words[groups[first].first:groups[last].last]
}
