package ninja

import "testing"

func TestTobiramaYongyeAvatarPolicyIsExactAndStrict(t *testing.T) {
	for _, name := range []string{TobiramaYongye, "千手扉间", "千手扉间[秽土转生]", "千手扉间[漂泊武士]", "千手扉间[少年]"} {
		got := avatarReadout(AvatarMatch{Name: name, Score: .99})
		slots, offset := 0, float64(0)
		if name == TobiramaYongye {
			slots, offset = 4, 9
		}
		if got.Name != name || got.Slots != slots || got.RowOffsetY != offset || got.Palette != "" ||
			avatarVariantPolicy(name) != (name == TobiramaYongye) || DualCooldown(name) {
			t.Fatalf("unreviewed Tobirama variant/palette/cooldown: %+v", got)
		}
	}
	if ShortLabel(TobiramaYongye) != "扉间·永夜逐光" {
		t.Fatal(ShortLabel(TobiramaYongye))
	}
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil || c.byID["90553"] == nil || c.byID["90553"].name != TobiramaYongye {
		t.Fatalf("official 90553 portrait missing: %v", err)
	}
	for _, score := range []float64{.95, .92} {
		s := newAvatarScores(2)
		s.best["90553"], s.name["90553"] = score, TobiramaYongye
		s.best["90025"], s.name["90025"] = 1.87-score, "千手扉间"
		if got := s.decide(AvatarMatch{}); got.ID != "" {
			t.Fatalf("ambiguous portrait moved the bean row: %+v", got)
		}
	}
}
