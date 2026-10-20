package win

import (
	"bytes"
	"image"
	"image/color"
	"math/rand/v2"
	"testing"
)

// referenceBGRA is the former CaptureClient conversion loop.
func referenceBGRA(src []byte, ww, wh, ox, oy, cw, ch int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, cw, ch))
	for y := 0; y < ch; y++ {
		sy := oy + y
		if sy < 0 || sy >= wh {
			continue
		}
		for x := 0; x < cw; x++ {
			sx := ox + x
			if sx < 0 || sx >= ww {
				continue
			}
			i := (sy*ww + sx) * 4
			img.SetRGBA(x, y, color.RGBA{R: src[i+2], G: src[i+1], B: src[i], A: 255})
		}
	}
	return img
}

func TestBGRAToRGBAEqualsPerPixelLoop(t *testing.T) {
	r := rand.New(rand.NewPCG(6, 7))
	for _, tc := range [][6]int{{1, 1, 0, 0, 1, 1}, {3, 2, 0, 0, 3, 2}, {9, 7, 1, 2, 5, 4}, {16, 9, 8, 1, 8, 8}, {1936, 1119, 8, 31, 1920, 1080}, {401, 300, 0, 0, 401, 300}} {
		ww, wh, ox, oy, cw, ch := tc[0], tc[1], tc[2], tc[3], tc[4], tc[5]
		src := make([]byte, ww*wh*4)
		for i := range src {
			src[i] = uint8(r.IntN(256))
		}
		want := referenceBGRA(src, ww, wh, ox, oy, cw, ch)
		got := image.NewRGBA(image.Rect(0, 0, cw, ch))
		for i := range got.Pix {
			got.Pix[i] = 0x5a // a reused buffer: every pixel must be overwritten
		}
		bgraToRGBA(got, src, ww, ox, oy)
		if !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("%v: conversion differs", tc)
		}
	}
}

func BenchmarkBGRAToRGBA1080p(b *testing.B) {
	const ww, wh = 1936, 1119
	src := make([]byte, ww*wh*4)
	b.Run("per-pixel", func(b *testing.B) {
		for b.Loop() {
			referenceBGRA(src, ww, wh, 8, 31, 1920, 1080)
		}
	})
	b.Run("words", func(b *testing.B) {
		dst := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
		for b.Loop() {
			bgraToRGBA(dst, src, ww, 8, 31)
		}
	})
}
