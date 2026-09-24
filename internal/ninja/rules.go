// Package ninja describes visual HUD variants and the user-confirmed cooldown
// policy. Archived skill tables are not authoritative for live timer rules.
package ninja

import (
	"strings"
	"time"
	"unicode"
)

const (
	NarutoStudent = "漩涡鸣人[忍者学员]"
	FifthMizukage = "照美冥[五代目水影]"
	Hashirama     = "千手柱间[木叶创立]"
	Madara        = "宇智波斑[神驹佑将]"
	// MadaraLegacyAlias is retained for old local fixtures only. It is an exact
	// spelling alias, not a relaxed match for other Madara variants.
	MadaraLegacyAlias = "宇智波斑[神驹佑祥]"
	Obito             = "宇智波带土[十尾人柱力]"
	SasukeXiayin      = "宇智波佐助[侠隐江湖]"
	Naruto            = "漩涡鸣人[暴怒·第六尾]"
	ItachiHyakusen    = "宇智波鼬[百战]"
	MinatoKyubi       = "波风水门[九喇嘛连结]"
	HashiramaEdo      = "千手柱间[秽土转生]"
	// EnergyGaugeRowOffset is measured from reviewed native HUDs. The energy
	// gauge pushes these ordinary four-diamond rows down 13px at 960x540. It is
	// active only after the complete title template is verified.
	EnergyGaugeRowOffset = 13
	DefaultCooldown      = 15 * time.Second
	AlternateCooldown    = 10 * time.Second
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
		return "斑·神驹佑将"
	case normalize(MadaraLegacyAlias):
		return "斑·神驹佑将"
	case normalize(Obito):
		return "带土·十尾"
	case normalize(SasukeXiayin):
		return "佐助·侠隐江湖"
	case normalize(NarutoStudent):
		return "鸣人·忍者学员"
	case normalize(Naruto):
		return "鸣人·第六尾"
	case normalize(ItachiHyakusen):
		return "鼬·百战"
	case normalize(MinatoKyubi):
		return "水门·九喇嘛连结"
	case normalize(HashiramaEdo):
		return "柱间·秽土转生"
	}
	text := []rune(name)
	if len(text) > 10 {
		return string(text[:9]) + "…"
	}
	return name
}
