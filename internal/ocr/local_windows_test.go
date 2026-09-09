//go:build windows && amd64 && cgo

package ocr

import (
	"context"
	"image"
	"testing"
	"time"
)

func TestLocalCTCBlankRepeatAndUncertainGlyph(t *testing.T) {
	chars := []string{"", "甲", "乙"}
	values := []float32{.1, .8, .1, .1, .85, .05, .9, .05, .05, .1, .8, .1, .33, .33, .34}
	line := decodeCharacters(values, 5, chars, image.Rect(10, 20, 210, 60))
	if line.Text != "甲甲�" {
		t.Fatalf("%+v", line)
	}
	for _, word := range line.Words {
		if word.X < 10 || word.X+word.Width > 210 || word.Width <= 0 {
			t.Fatalf("invalid box: %+v", word)
		}
	}
}

func TestLocalWorkerCloseAndRestart(t *testing.T) {
	r := NewLocal()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	img := image.NewRGBA(image.Rect(0, 0, 200, 30))
	if _, err := r.Read(ctx, img); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(ctx, img); err == nil {
		t.Fatal("closed worker accepted request")
	}
	r = NewLocal()
	defer r.Close()
	if _, err := r.Read(ctx, img); err != nil {
		t.Fatal(err)
	}
}
