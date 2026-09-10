package scene

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"narutotimer/assets"
	"narutotimer/internal/config"
	"os"
	"path/filepath"
	"reflect"
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

func TestDefaultManifestIgnoresLeftoverExternalResources(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Default()
	want, err := Load(cfg)
	if err != nil || want == nil {
		t.Fatalf("embedded baseline: %v, %v", want, err)
	}
	data, err := assets.Templates.ReadFile("templates/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(DefaultManifest), 0o755); err != nil {
		t.Fatal(err)
	}
	// Even when the external manifest is valid, its stale images and masks must
	// not get mixed with the EXE's template specifications.
	for _, item := range want.templates {
		for _, name := range []string{item.spec.File, item.spec.Mask} {
			if name == "" {
				continue
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(DefaultManifest), name), []byte("obsolete image"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, old := range []struct {
		name string
		data []byte
	}{
		{"invalid-json", []byte("{")},
		{"stale-empty-manifest", []byte(`{"templates":[]}`)},
		{"valid-manifest-obsolete-images", data},
	} {
		t.Run(old.name, func(t *testing.T) {
			if err := os.WriteFile(DefaultManifest, old.data, 0o644); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"", DefaultManifest, "./" + DefaultManifest} {
				cfg.Scene.Manifest = path
				got, err := Load(cfg)
				if err != nil || got == nil {
					t.Fatalf("default %q: %v, %v", path, got, err)
				}
				if !reflect.DeepEqual(got.templates, want.templates) || !reflect.DeepEqual(got.minimumRegions, want.minimumRegions) {
					t.Fatalf("default %q changed embedded ROI, template image or mask", path)
				}
			}
		})
	}
}

func TestExplicitCustomManifestLoadsItsOwnResources(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := "custom"
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gray := image.NewGray(image.Rect(0, 0, 3, 2))
	copy(gray.Pix, []byte{20, 40, 60, 80, 100, 120})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, gray); err != nil {
		t.Fatal(err)
	}
	spec := TemplateSpec{
		ID: "custom-marker", Scene: "fight", File: "custom.png", Mask: "custom-mask.png",
		ROI: config.NormalizedRect{X: 0.1, Y: 0.2, Width: 0.3, Height: 0.4}, Threshold: 0.7,
	}
	for _, name := range []string{spec.File, spec.Mask} {
		if err := os.WriteFile(filepath.Join(dir, name), encoded.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := json.Marshal(Manifest{Templates: []TemplateSpec{spec}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{path, abs} {
		cfg := config.Default()
		cfg.Scene.Manifest = path
		cat, err := Load(cfg)
		if err != nil || cat == nil {
			t.Fatalf("custom %q: %v, %v", path, cat, err)
		}
		if len(cat.templates) != 1 || !reflect.DeepEqual(cat.templates[0].spec, spec) {
			t.Fatalf("custom %q ignored its ID or ROI", path)
		}
		if !reflect.DeepEqual(cat.templates[0].gray, gray) || !reflect.DeepEqual(cat.templates[0].mask, gray) {
			t.Fatalf("custom %q did not load images beside its manifest", path)
		}
	}
}

func TestExplicitCustomManifestKeepsMissingAndInvalidBehavior(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Default()
	cfg.Scene.Manifest = "custom-manifest.json"
	cat, err := Load(cfg)
	if err != nil || cat != nil {
		t.Fatalf("missing custom manifest must allow color fallback: %v, %v", cat, err)
	}
	if err := os.WriteFile(cfg.Scene.Manifest, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cat, err = Load(cfg); err == nil || cat != nil {
		t.Fatalf("invalid custom manifest must report its error: %v, %v", cat, err)
	}
}
