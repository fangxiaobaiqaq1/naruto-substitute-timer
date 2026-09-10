//go:build windows

package updates

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyReplacesOnlyExeAndRetainsRecoverableOldVersion(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("LOCALAPPDATA", cache)
	dir := t.TempDir()
	target := filepath.Join(dir, "timer-app.exe")
	incoming := filepath.Join(Directory(), "download", "timer-app.exe")
	os.MkdirAll(filepath.Dir(incoming), 0700)
	old := []byte("previous executable")
	next := []byte("new verified executable")
	os.WriteFile(target, old, 0700)
	os.WriteFile(incoming, next, 0700)
	config := []byte(`{"player":"preserve me"}`)
	os.WriteFile(filepath.Join(dir, "config.json"), config, 0600)
	hash := sha256.Sum256(next)
	p := Plan{Parent: 0xfffffffc, Target: target, Incoming: incoming, Hash: hex.EncodeToString(hash[:]), Size: int64(len(next)), Version: "v0.2.0"}
	if e := apply(p, false); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(next) {
		t.Fatal("new executable not installed")
	}
	got, _ = os.ReadFile(target + ".previous")
	if string(got) != string(old) {
		t.Fatal("backup lost")
	}
	got, _ = os.ReadFile(filepath.Join(dir, "config.json"))
	if string(got) != string(config) {
		t.Fatal("settings changed")
	}
	os.WriteFile(incoming, []byte("tampered"), 0700)
	if e := apply(p, false); e == nil {
		t.Fatal("tampered update installed")
	}
	got, _ = os.ReadFile(target)
	if string(got) != string(next) {
		t.Fatal("failed update touched current binary")
	}
}
