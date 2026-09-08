package hudtext

import (
	"strings"
	"unicode"
)

// OCR must match a complete known HUD name. Allowing arbitrary Han prefixes
// turned scenery read as "一" into a confirmed "一神秘面具男". Catalog short
// names are expanded only through the separately documented exact HUD names.
func (d Dictionary) knownBase(text string) bool {
	if d[text] {
		return true
	}
	for _, entry := range hudNameExpansions {
		if text == entry.Name && d[entry.CatalogBase] {
			return true
		}
	}
	return false
}

// Candidates are display-only: at least four library glyphs, at most one
// substitution, unique best match at the title's START. Short library names,
// missing glyphs, nickname searches and inferred versions are deliberately
// excluded rather than manufacturing a match for a particular screenshot.
func (d Dictionary) candidate(raw string) string {
	text := strings.TrimLeftFunc(compact(raw), func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
	if i := strings.IndexAny(text, "(["); i >= 0 {
		text = text[:i]
	}
	glyphs := []rune(text)
	best, bestCost := "", 2
	ambiguous := false
	for name := range d {
		want := []rune(name)
		if len(want) < 4 || len(want) > 10 || len(glyphs) < len(want) {
			continue
		}
		cost := 0
		for i, r := range want {
			if glyphs[i] != r {
				cost++
			}
		}
		if cost > 1 {
			continue
		}
		if cost < bestCost {
			best, bestCost, ambiguous = name, cost, false
		} else if cost == bestCost && name != best {
			ambiguous = true
		}
	}
	if ambiguous {
		return ""
	}
	return best
}
