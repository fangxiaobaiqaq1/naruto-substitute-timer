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
