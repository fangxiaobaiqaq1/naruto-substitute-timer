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
