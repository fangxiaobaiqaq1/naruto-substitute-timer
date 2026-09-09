package hudtext

import (
	"context"
	"fmt"
	"image"
	"narutotimer/internal/config"
	"narutotimer/internal/match"
	"narutotimer/internal/ocr"
)

type Report struct {
	Titles   [2]Title            `json:"titles"`
	Variants [2][variants]string `json:"variants"`
}

// Diagnose is an explicit, offline inspection path. Live recognition uses the
// bounded asynchronous wrapper instead and does not log or persist raw text.
func Diagnose(ctx context.Context, reader ocr.Recognizer, img *image.RGBA, cfg config.Config, profile string) (Report, error) {
	var report Report
	regions, ok := nameRegions(img, cfg.Layout, profile)
	if !ok {
		return report, fmt.Errorf("unsupported OCR HUD geometry")
	}
	var strips [2]strip
	for i, roi := range regions {
		strips[i].img = match.CropRGBA(img, roi)
	}
	sheet := buildSheet(strips)
	lines, err := sheet.read(ctx, reader)
	if err != nil {
		return report, err
	}
	report.Variants = sheet.texts(lines)
	dict := NewDictionary(cfg.UI.SubstituteTable)
	for i, raw := range report.Variants {
		report.Titles[i] = dict.consensus(raw)
	}
	return report, nil
}
