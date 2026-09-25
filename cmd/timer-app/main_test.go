package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplicationDirectory(t *testing.T) {
	root := t.TempDir()
	if got := applicationDirectory(filepath.Join(root, "timer-app.exe")); got != root {
		t.Fatal(got)
	}
	bin := filepath.Join(root, "bin")
	if got := applicationDirectory(filepath.Join(bin, "timer-app.exe")); got != bin {
		t.Fatal(got)
	}
	if err := os.WriteFile(filepath.Join(root, "config.example.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := applicationDirectory(filepath.Join(bin, "timer-app.exe")); got != root {
		t.Fatal(got)
	}
}

func TestLoadInitialConfigKeepsMissingConfigInMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, firstRun, err := loadInitialConfig(path)
	if err != nil || !firstRun {
		t.Fatalf("first run = %v, err = %v", firstRun, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing config was unexpectedly written: %v", err)
	}
	if cfg.SchemaVersion == 0 {
		t.Fatal("missing config did not return defaults")
	}
	if err := os.WriteFile(path, []byte(`{"schemaVersion": 1}`), 0600); err != nil {
		t.Fatal(err)
	}
	// A malformed existing file must not be treated as first run or overwritten.
	if _, firstRun, err := loadInitialConfig(path); err == nil || firstRun {
		t.Fatalf("first run = %v, err = %v", firstRun, err)
	}
}

func TestInstalledAppUsesUserDataAndPortableKeepsOwnConfig(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	t.Setenv("APPDATA", data)
	exe := filepath.Join(root, "timer-app.exe")
	if got, e := writableApplicationDirectory(exe); e != nil || got != root {
		t.Fatal(got, e)
	}
	if e := os.WriteFile(filepath.Join(root, "installed.marker"), []byte("installed"), 0600); e != nil {
		t.Fatal(e)
	}
	got, e := writableApplicationDirectory(exe)
	if e != nil || got != filepath.Join(data, "NarutoTimer") {
		t.Fatal(got, e)
	}
}
