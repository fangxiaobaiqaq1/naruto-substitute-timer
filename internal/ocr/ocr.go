// Package ocr provides an optional local text recognizer. No network, account
// credentials, game memory access, or input automation is involved.
package ocr

import (
	"context"
	"image"
)

type Word struct {
	Text   string  `json:"text"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"w"`
	Height float64 `json:"h"`
}

type Line struct {
	Text  string `json:"text"`
	Words []Word `json:"words"`
}

// Recognizer is called only by one bounded background worker. Close must be
// safe during a blocked Read and cancel it instead of waiting indefinitely.
type Recognizer interface {
	Read(context.Context, image.Image) ([]Line, error)
	Close() error
}

// RegionRecognizer reads the supplied text lines without detecting unrelated
// scenery. Returned word coordinates stay in the original image coordinates.
type RegionRecognizer interface {
	ReadRegions(context.Context, image.Image, []image.Rectangle) ([]Line, error)
}
