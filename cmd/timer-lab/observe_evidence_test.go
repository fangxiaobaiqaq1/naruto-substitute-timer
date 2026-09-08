package main

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"testing"

	"narutotimer/internal/frame"
)

func diagnosticKnownFrame() frame.Frame {
	f := frame.Frame{Img: image.NewRGBA(image.Rect(0, 0, 2, 2)), Fighting: true, LeftSlots: 4, RightSlots: 4}
	for _, s := range []string{"L", "R"} {
		for i := 1; i <= 4; i++ {
			f.Beads = append(f.Beads, frame.Bead{Label: fmt.Sprintf("%s%d", s, i), Lit: true})
		}
	}
	return f
}

func TestDiagnosticEvidenceIsOptInBoundedAndDeepCopied(t *testing.T) {
	f := diagnosticKnownFrame()
	var disabled diagnosticFrames
	if disabled.keep(f) != "" || disabled.bytes != 0 {
		t.Fatal("default diagnostic saved pixels")
	}
	d := diagnosticFrames{limit: 2}
	source := image.NewRGBA(image.Rect(8, 9, 12, 13))
	source.SetRGBA(9, 10, color.RGBA{12, 34, 56, 255})
	f.Img = source.SubImage(image.Rect(9, 10, 11, 12)).(*image.RGBA)
	if d.keep(f) != "diagnostic-01.png" {
		t.Fatal("missing baseline")
	}
	source.SetRGBA(9, 10, color.RGBA{99, 99, 99, 255})
	if d.frames[0].img.Bounds() != image.Rect(0, 0, 2, 2) || d.frames[0].img.RGBAAt(0, 0) != (color.RGBA{12, 34, 56, 255}) {
		t.Fatal("retained borrowed/nonzero-origin buffer")
	}
	if d.keep(f) != "" {
		t.Fatal("saved every healthy frame")
	}
	f.Beads[0].Unknown = true
	if d.keep(f) != "diagnostic-02.png" || d.bytes != 32 {
		t.Fatal("did not retain unknown side within budget")
	}
	if d.keep(f) != "" {
		t.Fatal("exceeded requested count")
	}
	d = diagnosticFrames{limit: 100}
	f.Hold = true
	for range 10 {
		d.keep(f)
	}
	if len(d.frames) != 8 {
		t.Fatal("exceeded hard evidence count")
	}
}

func TestDiagnosticEvidenceRejectsErrorsAndOversizedImages(t *testing.T) {
	f := diagnosticKnownFrame()
	for _, tc := range []struct {
		name string
		img  *image.RGBA
		err  error
	}{
		{"nil", nil, nil}, {"empty", &image.RGBA{}, nil},
		{"oversized", &image.RGBA{Rect: image.Rect(0, 0, 8192, 8192)}, nil},
		{"capture error", f.Img, errors.New("capture lost")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := diagnosticFrames{limit: 8}
			f.Img, f.Err = tc.img, tc.err
			if d.keep(f) != "" || d.bytes != 0 {
				t.Fatal("retained invalid evidence")
			}
		})
	}
	f = diagnosticKnownFrame()
	d := diagnosticFrames{limit: 8, bytes: (64 << 20) - 16}
	if d.keep(f) == "" || d.bytes != 64<<20 {
		t.Fatal("exact remaining budget rejected")
	}
	f.Hold = true
	if d.keep(f) != "" {
		t.Fatal("exceeded aggregate memory budget")
	}
}
