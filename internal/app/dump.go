package app

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/draw"
	"narutotimer/internal/frame"
)

// SaveBeadDump writes the capture and an annotated copy into debug.directory
// ("debug" if empty). Names carry milliseconds and a counter, so quick dumps
// never overwrite each other; each PNG is written to a temporary sibling and
// renamed, so an interrupted dump never leaves a truncated file. Only the
// newest debug.retentionFiles dump files are kept (0 keeps all).
func SaveBeadDump(debug config.DebugConfig, img *image.RGBA, beads []frame.Bead) (string, error) {
	if img == nil {
		return "", fmt.Errorf("没有截图")
	}
	dir := debug.Directory
	if dir == "" {
		dir = "debug"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-150405.000")
	base := ""
	for i := 1; base == "" || exists(filepath.Join(dir, base+".png")) || exists(filepath.Join(dir, base+"-raw.png")); i++ {
		base = fmt.Sprintf("dump-%s-%02d", stamp, i) // Name order stays age order.
	}
	rawPath := filepath.Join(dir, base+"-raw.png")
	annPath := filepath.Join(dir, base+".png")

	raw := draw.CopyRGBA(img)
	if err := writePNG(rawPath, raw); err != nil {
		return "", err
	}

	ann := draw.CopyRGBA(img)
	for _, b := range beads {
		c := color.RGBA{R: 180, G: 180, B: 180, A: 255}
		switch {
		case b.Gold:
			c = color.RGBA{R: 255, G: 196, B: 40, A: 255}
		case !b.Dark:
			c = color.RGBA{R: 40, G: 210, B: 255, A: 255}
		case b.Dark:
			c = color.RGBA{R: 80, G: 110, B: 255, A: 255}
		}
		draw.BeadMarkerState(ann, b.X, b.Y, b.Label, c)
	}
	if err := writePNG(annPath, ann); err != nil {
		return "", err
	}
	pruneDumps(dir, debug.RetentionFiles)
	return annPath, nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// pruneDumps removes the oldest dump files beyond keep. Names start with a
// fixed-width timestamp, so name order is age order.
func pruneDumps(dir string, keep int) {
	if keep <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var dumps []string
	for _, e := range entries {
		if name := e.Name(); e.Type().IsRegular() && strings.HasPrefix(name, "dump-") && strings.HasSuffix(name, ".png") {
			dumps = append(dumps, name)
		}
	}
	sort.Strings(dumps)
	for _, name := range dumps[:max(0, len(dumps)-keep)] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

func writePNG(path string, img *image.RGBA) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := png.Encode(tmp, img); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
