package scene

import (
	"encoding/json"
	"narutotimer/assets"
	"narutotimer/internal/config"
	"testing"
)

func TestStandaloneLoadsEntireEmbeddedManifest(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Default()
	cat, err := Load(cfg)
	if err != nil || cat == nil {
		t.Fatalf("standalone load: %v, %v", cat, err)
	}
	data, err := assets.Templates.ReadFile("templates/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(cat.templates) != len(manifest.Templates) {
		t.Fatalf("loaded %d of %d templates", len(cat.templates), len(manifest.Templates))
	}
	for _, item := range cat.templates {
		if item.gray == nil || (item.spec.Mask != "" && item.mask == nil) {
			t.Fatalf("missing resource: %s", item.spec.ID)
		}
	}
}
