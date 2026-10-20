package mumu

import (
	"image"
	"unsafe"
)

// frameRing is how many delivered frames stay valid at once: the frame being
// processed plus the one the next capture writes. The capture scheduler hands
// one frame to the UI and requests the next only after that frame was
// processed (one capture in flight, the UI consumes each frame synchronously).
// Anything that keeps pixels longer copies them synchronously: replay and
// diagnostics, calibration snapshots, dumps, OCR strips and the identity
// opponent crop.
const frameRing = 2

// frameRingBuffer reuses output images instead of allocating ~8 MiB per 1080p
// capture. That allocation stream was a major source of GC work at 60 Hz.
// The SDK writes straight into the next slot (no separate staging buffer);
// the slot is handed out only after a successful capture.
type frameRingBuffer struct {
	frames [frameRing]*image.RGBA
	next_  int
	row    []byte // one-row scratch for the in-place flip
}

// slot returns the buffer the next capture fills, reallocating only after a
// resize. It does not advance the ring.
func (r *frameRingBuffer) slot(width, height int) *image.RGBA {
	out := r.frames[r.next_]
	if out == nil || out.Rect.Dx() != width || out.Rect.Dy() != height {
		out = image.NewRGBA(image.Rect(0, 0, width, height))
		r.frames[r.next_] = out
	}
	return out
}

// advance hands out the current slot: the following capture uses the next.
func (r *frameRingBuffer) advance() { r.next_ = (r.next_ + 1) % frameRing }

// next returns the next ring buffer, reallocating only after a resize.
func (r *frameRingBuffer) next(width, height int) *image.RGBA {
	out := r.slot(width, height)
	r.advance()
	return out
}

// flipOpaqueInPlace turns the bottom-up rows the SDK wrote into img (packed,
// Stride == 4*width) into top-down rows with opaque alpha: exactly the pixels
// fromBottomUpRGBAInto produces from a separate source buffer, in one pass.
// When hash is non-nil it returns hashes with hashes[y] = hash(row y) of the
// final pixels, computed while each row is still in cache.
func (r *frameRingBuffer) flipOpaqueInPlace(img *image.RGBA, hash func([]byte) uint64, hashes []uint64) []uint64 {
	width, height := img.Rect.Dx(), img.Rect.Dy()
	stride := width * 4
	if hash != nil {
		if cap(hashes) < height {
			hashes = make([]uint64, height)
		}
		hashes = hashes[:height]
	}
	if cap(r.row) < stride {
		r.row = make([]byte, stride)
	}
	tmp := r.row[:stride]
	for y := 0; y < height/2; y++ {
		top := img.Pix[y*stride : (y+1)*stride]
		bottom := img.Pix[(height-1-y)*stride : (height-y)*stride]
		copy(tmp, top)
		opaqueCopy(top, bottom)
		opaqueCopy(bottom, tmp)
		if hash != nil {
			hashes[y], hashes[height-1-y] = hash(top), hash(bottom)
		}
	}
	if height%2 == 1 {
		y := height / 2
		middle := img.Pix[y*stride : (y+1)*stride]
		opaqueCopy(middle, middle)
		if hash != nil {
			hashes[y] = hash(middle)
		}
	}
	return hashes
}

// opaqueCopy stores src into dst with every alpha byte set to 0xff. Both hold
// whole RGBA pixels at 4-byte aligned offsets of a Pix buffer; dst may be src.
func opaqueCopy(dst, src []byte) {
	if len(src) == 0 {
		return
	}
	if unsafe.SliceData(dst) != unsafe.SliceData(src) {
		copy(dst, src)
	}
	p := dst[:len(src)]
	// Two pixels per aligned 64-bit word (little-endian: bytes 3 and 7).
	if uintptr(unsafe.Pointer(unsafe.SliceData(p)))%8 != 0 {
		p[3] = 0xff
		p = p[4:]
	}
	if n := len(p) / 8; n > 0 {
		words := unsafe.Slice((*uint64)(unsafe.Pointer(unsafe.SliceData(p))), n)
		for i := range words {
			words[i] |= 0xff000000ff000000
		}
	}
	if len(p)%8 != 0 {
		p[len(p)-1] = 0xff
	}
}
