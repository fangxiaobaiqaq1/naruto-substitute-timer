//go:build unix

package ui

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"narutotimer/internal/config"
)

// holdConfigFiles keeps config's file lock busy: a config.Load blocked reading
// a FIFO holds it, so a config save waits as if the disk write were slow
// (fsync, antivirus). The returned func releases it.
func holdConfigFiles(t *testing.T) (release func()) {
	t.Helper()
	fifo := filepath.Join(t.TempDir(), "slow.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no FIFO support:", err)
	}
	loaded := make(chan struct{})
	go func() {
		defer close(loaded)
		_, _ = config.Load(fifo)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		// Opening the write end succeeds only once Load has opened the read end,
		// i.e. while it holds the lock; Load then blocks reading.
		writer, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			return func() {
				writer.Close()
				<-loaded
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("config.Load never opened the FIFO")
		}
		time.Sleep(time.Millisecond)
	}
}

// GE-12: pressing 换边 (or any settings change) during a fight must not block
// the capture loop, which takes s.mu on every frame, for the length of the
// settings disk write.
func TestSettingsSaveDoesNotHoldSessionLockDuringDiskWrite(t *testing.T) {
	s := &session{cfg: config.Default(), cfgPath: filepath.Join(t.TempDir(), "config.json"), remember: true}
	release := holdConfigFiles(t)
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	saved := make(chan error, 1)
	go func() { saved <- s.selectSideMode("left") }()
	applied := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline) && !applied; time.Sleep(time.Millisecond) {
		if s.mu.TryLock() { // what captureOnce needs every frame
			applied = s.cfg.UI.PlayerSide == "left"
			s.mu.Unlock()
		}
	}
	select {
	case err := <-saved:
		t.Fatalf("save finished while the config file was busy: %v", err)
	default:
	}
	if !applied {
		t.Fatal("the session lock stayed held while the settings were being written")
	}
	release()
	released = true
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	stored, err := config.Load(s.cfgPath)
	if err != nil || stored.UI.PlayerSide != "left" {
		t.Fatalf("stored side %q, err %v", stored.UI.PlayerSide, err)
	}
}
