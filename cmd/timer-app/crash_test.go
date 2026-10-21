package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"narutotimer/internal/safego"
)

// GE-03: timer-app.exe is linked -H windowsgui, so a crash report on stderr is
// lost. Fatal panics from any goroutine and the panics recovered on the
// capture/analysis path must both reach debug/crash.log.
func TestCrashLogKeepsRecoveredAndFatalPanics(t *testing.T) {
	if dir := os.Getenv("TIMER_APP_CRASH_DIR"); dir != "" {
		if _, err := installCrashLog(dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		safego.Report("analysis", &safego.PanicError{Value: "recovered test panic", Stack: []byte("goroutine 9 [running]:")})
		done := make(chan struct{})
		go func() { panic("unrecovered test crash") }()
		<-done
	}
	dir := filepath.Join(t.TempDir(), "debug")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashLogKeepsRecoveredAndFatalPanics$")
	cmd.Env = append(os.Environ(), "TIMER_APP_CRASH_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("child did not crash:\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "crash.log"))
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, want := range []string{"recovered panic: recovered test panic in analysis", "panic: unrecovered test crash", "crash_test.go"} {
		if !strings.Contains(log, want) {
			t.Fatalf("crash.log lacks %q:\n%s", want, log)
		}
	}
}

// A crash loop must not grow the log without bound: an oversized log is kept
// once as crash.log.old and a new one is started.
func TestCrashLogRotatesOversizedLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crash.log")
	if err := os.WriteFile(path, make([]byte, crashLogLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := installCrashLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		safego.SetLog(nil)
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		f.Close()
	})
	old, err := os.Stat(path + ".old")
	if err != nil || old.Size() != crashLogLimit+1 {
		t.Fatalf("old log not kept: %v", err)
	}
	if current, err := os.Stat(path); err != nil || current.Size() != 0 {
		t.Fatalf("new log not started: %v", err)
	}
}
