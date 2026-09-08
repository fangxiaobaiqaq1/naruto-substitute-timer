// Package ninja describes visual HUD variants and the user-confirmed cooldown
// policy. Archived skill tables are not authoritative for live timer rules.
package ninja

import (
	"strings"
	"time"
	"unicode"
)

const (
	FifthMizukage     = "照美冥[五代目水影]"
	Hashirama         = "千手柱间[木叶创立]"
	Madara            = "宇智波斑[神驹佑祥]"
	Obito             = "宇智波带土[十尾人柱力]"
	Naruto            = "漩涡鸣人[暴怒·第六尾]"
	DefaultCooldown   = 15 * time.Second
	AlternateCooldown = 10 * time.Second
)

// DualCooldown matches the full version, never a bare name or an ambiguous
// numeric ID from the old extracted table. Bracket typography is insignificant.
func DualCooldown(name string) bool {
	return normalize(name) == normalize(FifthMizukage)
}

func normalize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("[]【】()（）·・", r) {
			return -1
		}
		return r
	}, s)
}

func ShortLabel(name string) string {
	switch normalize(name) {
	case normalize(FifthMizukage):
		return "五代目水影"
	case normalize(Hashirama):
		return "柱间·木叶创立"
	case normalize(Madara):
		return "斑·神驹佑祥"
	case normalize(Obito):
		return "带土·十尾"
	case normalize(Naruto):
		return "鸣人·第六尾"
	}
	text := []rune(name)
	if len(text) > 10 {
		return string(text[:9]) + "…"
	}
	return name
}
