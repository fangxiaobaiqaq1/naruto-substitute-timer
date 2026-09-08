package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSaveNeverExposesPartialJSONToReaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default()
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	errs := make(chan error, 1)
	var readers sync.WaitGroup
	for range 3 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := Load(path); err != nil {
					select {
					case errs <- err:
					default:
					}
					return
				}
			}
		})
	}
	for i := range 40 {
		cfg.UI.PlayerSide = []string{"auto", "left", "right"}[i%3]
		cfg.UI.NinjaQuery = fmt.Sprintf("saved-generation-%d", i)
		if err := Save(path, cfg); err != nil {
			t.Errorf("save %d: %v", i, err)
			break
		}
	}
	close(stop)
	readers.Wait()
	select {
	case err := <-errs:
		t.Fatalf("reader saw missing/truncated configuration during Save: %v", err)
	default:
	}
	got, err := Load(path)
	if err != nil || got.UI.NinjaQuery != cfg.UI.NinjaQuery {
		t.Fatalf("last save not durable: %q err=%v", got.UI.NinjaQuery, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v", entries)
	}
}

func TestSaveFailurePreservesPreviousConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := Default()
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.PlayerSide = "invalid"
	if err := Save(path, cfg); err == nil {
		t.Fatal("expected validation failure")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed save destroyed previous config")
	}
	destination := filepath.Join(dir, "target-directory")
	if err := os.Mkdir(destination, 0755); err != nil {
		t.Fatal(err)
	}
	if err := Save(destination, Default()); err == nil {
		t.Fatal("expected commit failure on directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("failed commit leaked temporary file: %v", entries)
	}
}
