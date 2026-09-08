//go:build windows && amd64

package mumu

import "testing"

func TestBottomUpRGBAIsOwnedAndKeepsChannels(t *testing.T) {
	src := []byte{1, 2, 3, 0, 4, 5, 6, 0, 7, 8, 9, 0, 10, 11, 12, 0, 13, 14, 15, 0, 16, 17, 18, 0}
	img := fromBottomUpRGBA(src, 3, 2)
	if p := img.RGBAAt(0, 0); p.R != 10 || p.G != 11 || p.B != 12 || p.A != 255 {
		t.Fatalf("top left: %v", p)
	}
	if p := img.RGBAAt(2, 1); p.R != 7 || p.G != 8 || p.B != 9 {
		t.Fatalf("bottom right: %v", p)
	}
	src[12] = 99
	if img.RGBAAt(0, 0).R != 10 {
		t.Fatal("SDK buffer must not alias the delivered frame")
	}
}
