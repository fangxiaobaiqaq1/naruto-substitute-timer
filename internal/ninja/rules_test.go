package ninja

import "testing"

func TestMadaraUsesCanonicalCorpusSpelling(t *testing.T) {
	if Madara != "宇智波斑[神驹佑将]" {
		t.Fatalf("canonical Madara=%q", Madara)
	}
	if !sameAvatarVariant(Madara, MadaraLegacyAlias) {
		t.Fatal("legacy Madara spelling should remain an exact compatibility alias")
	}
	for _, name := range []string{"宇智波斑", "宇智波斑[秽土转生]", "宇智波斑[神驹佑祥]其他", "宇智波斑[骥玄凌霄]"} {
		if sameAvatarVariant(Madara, name) {
			t.Fatalf("unrelated Madara variant matched: %q", name)
		}
	}
	if got := ShortLabel(MadaraLegacyAlias); got != "斑·神驹佑将" {
		t.Fatalf("legacy label=%q", got)
	}
}

func TestDualCooldownIsOnlyFifthMizukage(t *testing.T) {
	for _, name := range []string{FifthMizukage, "照美冥【五代目水影】", " 照美冥 （五代目水影） "} {
		if !DualCooldown(name) {
			t.Fatalf("missed exact variant %q", name)
		}
	}
	for _, name := range []string{"", "照美冥", "照美冥[泳装]", "五代目水影", "908290", "909440", "980230", Hashirama, Madara, Obito, Naruto, ItachiHyakusen, MinatoKyubi, HashiramaEdo, "照美冥[五代目水影][其他版本]"} {
		if DualCooldown(name) {
			t.Fatalf("overbroad special rule: %q", name)
		}
	}
}
