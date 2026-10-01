package mumu

import (
	"image"
	"unsafe"
)

// frameRing is how many delivered frames stay valid at once. The capture
// scheduler hands one frame to the UI and requests the next only after that
// frame was processed; the extra slots protect consumers that finish slightly
// later. Anything that keeps pixels longer (replay, dumps, OCR crops) copies.
const frameRing = 3

// frameRingBuffer reuses output images instead of allocating ~8 MiB per 1080p
// capture. That allocation stream was a major source of GC work at 60 Hz.
type frameRingBuffer struct {
	frames [frameRing]*image.RGBA
	next_  int
}

// next returns the next ring buffer, reallocating only after a resize.
func (r *frameRingBuffer) next(width, height int) *image.RGBA {
	out := r.frames[r.next_]
	if out == nil || out.Rect.Dx() != width || out.Rect.Dy() != height {
		out = image.NewRGBA(image.Rect(0, 0, width, height))
		r.frames[r.next_] = out
	}
	r.next_ = (r.next_ + 1) % frameRing
	return out
}

// fromBottomUpRGBA keeps the allocating form for callers that need an owned copy.
func fromBottomUpRGBA(src []byte, width, height int) *image.RGBA {
	return fromBottomUpRGBAInto(image.NewRGBA(image.Rect(0, 0, width, height)), src)
}

// fromBottomUpRGBAInto flips the SDK's bottom-up rows into dst and forces the
// alpha channel opaque. The alpha pass writes whole 32-bit pixels instead of
// touching every fourth byte, which the compiler cannot vectorize.
func fromBottomUpRGBAInto(dst *image.RGBA, src []byte) *image.RGBA {
	width, height := dst.Rect.Dx(), dst.Rect.Dy()
	stride := width * 4
	for y := 0; y < height; y++ {
		copy(dst.Pix[y*stride:(y+1)*stride], src[(height-1-y)*stride:(height-y)*stride])
	}
	pixels := dst.Pix[:width*height*4]
	if len(pixels) == 0 {
		return dst
	}
	words := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(pixels))), len(pixels)/4)
	for i := range words {
		words[i] |= 0xff000000 // little-endian: byte 3 of each RGBA pixel
	}
	return dst
}
