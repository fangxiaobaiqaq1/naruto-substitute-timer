//go:build windows && amd64

package support

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"narutotimer/internal/config"
)

func TestExportBundleAllowListsSessionFiles(t *testing.T) {
	root := t.TempDir()
	session := filepath.Join(root, "session")
	if err := os.MkdirAll(session, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"session.json": "{}", "capture.jsonl": "{}\n", "report.json": "{}", "report.md": "# report", "secret.txt": "must not appear", "raw.png": "not included by default",
	} {
		if err := os.WriteFile(filepath.Join(session, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"schemaVersion":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := ExportBundle(context.Background(), BundleOptions{Root: root, SessionDir: session, Config: config.Default(), ConfigPath: configPath, Version: "v0.2.2"})
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	names := map[string]bool{}
	for _, file := range z.File {
		names[file.Name] = true
	}
	for _, want := range []string{"application.json", "environment.json", "config/runtime.json", "config/saved.json", "capture/state.json", "mumu/inventory.json", "summary.md", "manifest.json", "session/session.json", "session/capture.jsonl", "session/report.json", "session/report.md"} {
		if !names[want] {
			t.Fatalf("missing %s: %v", want, names)
		}
	}
	if names["session/secret.txt"] || names["session/raw.png"] || names["session/replay/frame.jpg"] {
		t.Fatalf("unexpected private/raw file: %v", names)
	}
}

func TestAllowedSessionFilesIncludesImagesOnlyByExplicitOptIn(t *testing.T) {
	session := t.TempDir()
	for dir, name := range map[string]string{"frames": "raw.png", "hud": "crop.png", "replay": "frame.jpg"} {
		if err := os.MkdirAll(filepath.Join(session, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(session, dir, name), []byte("image"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(session, "replay", "manifest.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session, "replay", "unfinished.jpg.partial"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(session, "replay", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session, "replay", "nested", "hidden.jpg"), []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	without := allowedSessionFiles(session, false)
	with := allowedSessionFiles(session, true)
	if len(without) != 1 || without[0] != "replay/manifest.json" {
		t.Fatalf("default allowlist leaked image: %v", without)
	}
	for _, want := range []string{"frames/raw.png", "hud/crop.png", "replay/frame.jpg", "replay/manifest.json"} {
		found := false
		for _, name := range with {
			found = found || name == want
		}
		if !found {
			t.Fatalf("explicit allowlist missing %s: %v", want, with)
		}
	}
	for _, forbidden := range []string{"replay/unfinished.jpg.partial", "replay/nested/hidden.jpg"} {
		for _, name := range with {
			if name == forbidden {
				t.Fatalf("recursive image allowlist escaped its directory: %v", with)
			}
		}
	}
}
func TestExportBundleDoesNotOverwriteEarlierBundle(t *testing.T) {
	root := t.TempDir()
	options := BundleOptions{Root: root, Config: config.Default(), Version: "v0.2.2"}
	first, err := ExportBundle(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ExportBundle(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("second export overwrote first bundle: %s", first)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("first bundle disappeared: %v", err)
	}
}
