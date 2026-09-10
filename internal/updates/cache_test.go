package updates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPruneCacheKeepsUnknownAndActiveFiles(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	root := Directory()
	old := filepath.Join(root, strings.Repeat("a", 64))
	active := filepath.Join(root, "apply-new")
	os.MkdirAll(old, 0700)
	os.MkdirAll(active, 0700)
	os.WriteFile(filepath.Join(old, "timer-app.exe"), []byte("old"), 0600)
	os.WriteFile(filepath.Join(old, "personal.txt"), []byte("keep"), 0600)
	os.WriteFile(filepath.Join(active, "updater.exe"), []byte("active"), 0600)
	past := time.Now().Add(-48 * time.Hour)
	os.Chtimes(filepath.Join(old, "timer-app.exe"), past, past)
	os.Chtimes(old, past, past)
	PruneCache(time.Now())
	if _, e := os.Stat(filepath.Join(old, "timer-app.exe")); !os.IsNotExist(e) {
		t.Fatal("old binary retained")
	}
	for _, p := range []string{filepath.Join(old, "personal.txt"), filepath.Join(active, "updater.exe")} {
		if _, e := os.Stat(p); e != nil {
			t.Fatal("unrelated/active file removed", e)
		}
	}
}
