package config

import "testing"

func TestBundledConfigDoesNotOverrideDuelProfile(t *testing.T) {
	cfg, err := Load("../../config.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, found := cfg.Layout.Profiles["duel"]; found {
		t.Fatal("bundled config must inherit the neutral built-in duel profile")
	}
	p := cfg.Layout.Profile("duel")
	for _, tc := range []struct {
		side string
		row  []NormalizedPoint
	}{
		{"left", p.Left.NominalCenters},
		{"right", p.Right.NominalCenters},
	} {
		for i, point := range tc.row {
			if point.Y != 103.0/900 {
				t.Fatalf("bundled duel %s center %d y=%g, want %g", tc.side, i+1, point.Y, 103.0/900)
			}
		}
	}
}
