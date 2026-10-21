package app

import (
	"image"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"narutotimer/internal/config"
	"narutotimer/internal/frame"
)

func dumpFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// GE-13: dumps go to debug.directory (not a hard-coded ./debug), quick dumps
// never overwrite each other (names had 1 s resolution), files appear whole
// (temporary sibling + rename) and only the newest debug.retentionFiles stay.
func TestSaveBeadDumpUsesConfiguredDirectoryAndUniqueNames(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	dir := filepath.Join(work, "custom-debug")
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	beads := []frame.Bead{{X: 10, Y: 10, Label: "L1", Lit: true}}
	var paths []string
	for i := 0; i < 3; i++ {
		path, err := SaveBeadDump(config.DebugConfig{Directory: dir}, img, beads)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(path) != dir {
			t.Fatalf("dump %d written to %s", i, path)
		}
		paths = append(paths, path)
	}
	if unique := slices.Compact(slices.Sorted(slices.Values(paths))); len(unique) != 3 {
		t.Fatalf("dumps overwrote each other: %v", paths)
	}
	names := dumpFiles(t, dir)
	if len(names) != 6 {
		t.Fatalf("want 3 raw + 3 annotated files, got %v", names)
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "dump-") || !strings.HasSuffix(name, ".png") {
			t.Fatalf("unexpected file %q (temporary file left behind?)", name)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "debug")); !os.IsNotExist(err) {
		t.Fatalf("hard-coded ./debug was used: %v", err)
	}
}

func TestSaveBeadDumpKeepsNewestRetentionFiles(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	var last string
	for i := 0; i < 5; i++ {
		path, err := SaveBeadDump(config.DebugConfig{Directory: dir, RetentionFiles: 4}, img, nil)
		if err != nil {
			t.Fatal(err)
		}
		last = path
	}
	names := dumpFiles(t, dir)
	if len(names) != 4 || !slices.Contains(names, filepath.Base(last)) {
		t.Fatalf("retention kept %v (newest %s)", names, filepath.Base(last))
	}
	unrelated := filepath.Join(dir, "notes.png")
	if err := os.WriteFile(unrelated, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveBeadDump(config.DebugConfig{Directory: dir, RetentionFiles: 2}, img, nil); err != nil {
		t.Fatal(err)
	}
	if names := dumpFiles(t, dir); len(names) != 3 || !slices.Contains(names, "notes.png") {
		t.Fatalf("retention must only remove old dumps: %v", names)
	}
}
