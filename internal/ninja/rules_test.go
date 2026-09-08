package ninja

import "testing"

func TestDualCooldownIsOnlyFifthMizukage(t *testing.T) {
	for _, name := range []string{FifthMizukage, "照美冥【五代目水影】", " 照美冥 （五代目水影） "} {
		if !DualCooldown(name) {
			t.Fatalf("missed exact variant %q", name)
		}
	}
	for _, name := range []string{"", "照美冥", "照美冥[泳装]", "五代目水影", "908290", "909440", "980230", Hashirama, Madara, Obito, Naruto, "照美冥[五代目水影][其他版本]"} {
		if DualCooldown(name) {
			t.Fatalf("overbroad special rule: %q", name)
		}
	}
}
