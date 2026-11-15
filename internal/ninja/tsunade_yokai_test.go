package ninja

import "testing"

func TestTsunadeYokaiAvatarPolicyIsExactAndStrict(t *testing.T) {
	for _, name := range []string{TsunadeYokai, "纲手", "纲手[百豪]", "纲手[侠隐江湖]", "纲手[幽森绮巡]"} {
		got := avatarReadout(AvatarMatch{Name: name, Score: .99})
		slots, offset := 0, float64(0)
		if name == TsunadeYokai {
			slots, offset = 4, 9
		}
		if got.Name != name || got.Slots != slots || got.RowOffsetY != offset || got.Palette != "" ||
			avatarVariantPolicy(name) != (name == TsunadeYokai) || DualCooldown(name) {
			t.Fatalf("unreviewed Tsunade variant/palette/cooldown: %+v", got)
		}
	}
	for _, score := range []float64{.95, .92} {
		s := newAvatarScores(2)
		s.best["90475"], s.name["90475"] = score, TsunadeYokai
		s.best["90012"], s.name["90012"] = 1.87-score, "纲手"
		if got := s.decide(AvatarMatch{}); got.ID != "" {
			t.Fatalf("ambiguous portrait moved the bean row: %+v", got)
		}
	}
}
