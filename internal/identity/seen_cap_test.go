package identity

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/imgsafe/imgsafetest"
)

func writeTestPNG(t *testing.T, path string, shade uint8, mod time.Time) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 12))
	for i := range img.Pix {
		img.Pix[i] = shade
	}
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func seenNames(book []Named) map[string]bool {
	out := map[string]bool{}
	for _, n := range book {
		if strings.HasPrefix(n.Name, seenCropPrefix) {
			out[n.Name] = true
		}
	}
	return out
}

// GE-05: LoadBook used to decode every crop in seen/ (1000 crops: 0.7 s and
// +141 MiB at start, then 8 s VS reads). Only the newest maxSeenCrops load.
func TestLoadBookLoadsOnlyNewestSeenCrops(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(SeenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, filepath.Join(AssetDir, "me.png"), 10, time.Now())
	base := time.Now().Add(-1000 * time.Hour)
	const total = maxSeenCrops + 37
	for i := 0; i < total; i++ {
		writeTestPNG(t, filepath.Join(SeenDir, fmt.Sprintf("opp-%06d.png", i)), uint8(i), base.Add(time.Duration(i)*time.Minute))
	}
	// A user-copied crop with an old name but a recent mtime is recent.
	if err := os.Chtimes(filepath.Join(SeenDir, "opp-000000.png"), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.UI.PlayerNames = []string{"me"}
	book := LoadBook(cfg)
	seen := seenNames(book)
	if len(seen) != maxSeenCrops {
		t.Fatalf("loaded %d seen crops, want %d", len(seen), maxSeenCrops)
	}
	if !seen["opp-000000"] || !seen[fmt.Sprintf("opp-%06d", total-1)] || seen["opp-000001"] || seen[fmt.Sprintf("opp-%06d", total-maxSeenCrops)] {
		t.Fatal("the loaded crops are not the most recently modified ones")
	}
	mine := 0
	for _, n := range book {
		if n.Mine {
			mine++
		}
	}
	if mine != 1 {
		t.Fatalf("own template lost: %d", mine)
	}
}

// GE-05: saves trim seen/ to the newest maxSeenCrops timer-written crops and
// never leave a partial PNG behind.
func TestSaveOppCropPrunesOldestCrops(t *testing.T) {
	t.Chdir(t.TempDir())
	saveMu.Lock()
	saved := lastSave
	lastSave = time.Time{}
	saveMu.Unlock()
	defer func() {
		saveMu.Lock()
		lastSave = saved
		saveMu.Unlock()
	}()
	if err := os.MkdirAll(SeenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-100 * time.Hour)
	for i := 0; i < maxSeenCrops+5; i++ {
		writeTestPNG(t, filepath.Join(SeenDir, fmt.Sprintf("opp-%06d.png", i)), uint8(i), base.Add(time.Duration(i)*time.Minute))
	}
	userFile := filepath.Join(SeenDir, "kept-by-user.png")
	writeTestPNG(t, userFile, 1, base.Add(-time.Hour))
	staleTmp := filepath.Join(SeenDir, ".opp-123.png.tmp")
	if err := os.WriteFile(staleTmp, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(staleTmp, base, base); err != nil {
		t.Fatal(err)
	}
	crop := image.NewRGBA(image.Rect(0, 0, 40, 12))
	saveOppCrop(crop)

	entries, err := os.ReadDir(SeenDir)
	if err != nil {
		t.Fatal(err)
	}
	var crops []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
		if strings.HasPrefix(e.Name(), seenCropPrefix) {
			crops = append(crops, e.Name())
		}
	}
	if len(crops) != maxSeenCrops {
		t.Fatalf("seen/ holds %d crops after a save, want %d", len(crops), maxSeenCrops)
	}
	for i := 0; i <= 5; i++ {
		if _, err := os.Stat(filepath.Join(SeenDir, fmt.Sprintf("opp-%06d.png", i))); !os.IsNotExist(err) {
			t.Fatalf("old crop %d kept: %v", i, err)
		}
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Fatalf("file not written by the timer was deleted: %v", err)
	}
	newest := filepath.Join(SeenDir, fmt.Sprintf("opp-%06d.png", maxSeenCrops+4))
	if _, err := os.Stat(newest); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(SeenDir, crops[len(crops)-1]))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := png.Decode(f); err != nil {
		t.Fatalf("saved crop is not a complete PNG: %v", err)
	}
}

// UR-11: a dimension bomb among the crops must be skipped, not decoded.
func TestLoadBookSkipsDimensionBomb(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(SeenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, filepath.Join(AssetDir, "me.png"), 10, time.Now())
	if err := os.WriteFile(filepath.Join(SeenDir, "opp-bomb.png"), imgsafetest.DimensionBombPNG(), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.UI.PlayerNames = []string{"me"}
	book := LoadBook(cfg)
	if len(book) != 1 || !book[0].Mine {
		t.Fatalf("book = %+v", book)
	}
}
