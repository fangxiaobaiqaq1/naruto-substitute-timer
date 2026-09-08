//go:build !windows

package ocr

import (
	"context"
	"fmt"
	"image"
)

type unavailable struct{}

func NewSystem() Recognizer { return unavailable{} }
func (unavailable) Read(context.Context, image.Image) ([]Line, error) {
	return nil, fmt.Errorf("system OCR is available only on Windows")
}
func (unavailable) Close() error { return nil }
