package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What config loading repaired (calibration boxes outside the size limits)
// must reach the debug log: the windowed build has no stderr.
func TestConfigNotesReachTheDebugLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	logConfigNotes(f, []string{"layout.calibration.leftAvatar: 头像框太大；已忽略这个框，这一区域按自动识别"}, at)
	logConfigNotes(nil, []string{"no log file: stderr only"}, at) // must not panic
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "2026-10-21T18:00:00Z config.json: layout.calibration.leftAvatar: 头像框太大；已忽略这个框，这一区域按自动识别\n"
	if string(data) != want {
		t.Fatalf("debug log = %q, want %q", data, want)
	}
	if strings.Contains(string(data), "stderr only") {
		t.Fatal("note without a log file reached the log")
	}
}
