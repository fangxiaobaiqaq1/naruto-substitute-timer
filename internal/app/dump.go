package app

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"narutotimer/internal/draw"
	"narutotimer/internal/frame"
)

func SaveBeadDump(img *image.RGBA, beads []frame.Bead) (string, error) {
	if img == nil {
		return "", fmt.Errorf("没有截图")
	}
	if err := os.MkdirAll("debug", 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-150405")
	rawPath := filepath.Join("debug", "dump-"+stamp+"-raw.png")
	annPath := filepath.Join("debug", "dump-"+stamp+".png")

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
	return annPath, nil
}

func writePNG(path string, img *image.RGBA) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
