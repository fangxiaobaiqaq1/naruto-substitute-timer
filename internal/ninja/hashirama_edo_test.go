package ninja

import "testing"

func TestHashiramaEdoAvatarPolicyIsExactAndStrict(t *testing.T) {
	for _, name := range []string{HashiramaEdo, "千手柱间", Hashirama, "千手柱间[侠隐江湖]", "千手柱间[初代目火影]"} {
		got := avatarReadout(AvatarMatch{Name: name, Score: .99})
		wantSlots, wantPalette := 0, Palette("")
		if name == HashiramaEdo {
			wantSlots, wantPalette = 6, Warm
		}
		if got.Name != name || got.Slots != wantSlots || got.Palette != wantPalette || got.RowOffsetY != 0 ||
			avatarVariantPolicy(name) != (name == HashiramaEdo) {
			t.Fatalf("%q must not inherit another variant's topology: %+v", name, got)
		}
	}
	// Neither an Edo winner nor an Edo runner-up may use the relaxed cosmetic
	// margin: a nearly identical portrait must not change 4 slots into 6.
	for _, edoScore := range []float64{.95, .92} {
		s := newAvatarScores(2)
		s.best["90102"], s.name["90102"] = edoScore, HashiramaEdo
		s.best["90024"], s.name["90024"] = 1.87-edoScore, "千手柱间"
		if got := s.decide(AvatarMatch{}); got.ID != "" {
			t.Fatalf("ambiguous Hashirama portrait enabled a policy: %+v", got)
		}
	}
}
