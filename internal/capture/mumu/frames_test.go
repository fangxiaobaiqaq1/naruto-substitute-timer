package mumu

import (
	"bytes"
	"hash/maphash"
	"image"
	"math/rand/v2"
	"testing"
	"unsafe"
)

// fromBottomUpRGBA is the former copying conversion (SDK staging buffer to an
// owned image); it is the oracle for the in-place flip.
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

func TestBottomUpRGBAFlipsRowsAndForcesOpaqueAlpha(t *testing.T) {
	src := []byte{1, 2, 3, 0, 4, 5, 6, 7, 7, 8, 9, 0, 10, 11, 12, 0, 13, 14, 15, 0, 16, 17, 18, 0}
	img := fromBottomUpRGBA(src, 3, 2)
	if p := img.RGBAAt(0, 0); p.R != 10 || p.G != 11 || p.B != 12 || p.A != 255 {
		t.Fatalf("top left: %v", p)
	}
	if p := img.RGBAAt(1, 1); p.R != 4 || p.G != 5 || p.B != 6 || p.A != 255 {
		t.Fatalf("bottom middle must keep colors and become opaque: %v", p)
	}
	src[12] = 99
	if img.RGBAAt(0, 0).R != 10 {
		t.Fatal("SDK buffer must not alias the delivered frame")
	}
}

func TestFrameRingReusesBuffersUntilResize(t *testing.T) {
	var ring frameRingBuffer
	first := ring.next(4, 2)
	var seen []uintptr
	for i := 0; i < frameRing; i++ {
		seen = append(seen, uintptr(0))
	}
	frames := make(map[*[]byte]bool)
	frames[&first.Pix] = true
	for i := 1; i < frameRing; i++ {
		frames[&ring.next(4, 2).Pix] = true
	}
	if len(frames) != frameRing {
		t.Fatalf("ring must hand out %d distinct buffers, got %d", frameRing, len(frames))
	}
	if again := ring.next(4, 2); again != first {
		t.Fatal("ring did not wrap to the first buffer")
	}
	if resized := ring.next(5, 2); resized.Rect.Dx() != 5 {
		t.Fatalf("resize must reallocate: %v", resized.Rect)
	}
	_ = seen
}

// The in-place flip must give exactly the pixels of the copying flip, for odd
// and even sizes, and hash every final row.
func TestFlipOpaqueInPlaceEqualsCopyingFlip(t *testing.T) {
	seed := maphash.MakeSeed()
	hash := func(b []byte) uint64 { return maphash.Bytes(seed, b) }
	r := rand.New(rand.NewPCG(3, 4))
	var ring frameRingBuffer
	var hashes []uint64
	for _, size := range [][2]int{{1, 1}, {1, 2}, {3, 2}, {3, 3}, {5, 7}, {64, 9}, {1920, 3}, {17, 1080}} {
		w, h := size[0], size[1]
		src := make([]byte, w*h*4)
		for i := range src {
			src[i] = uint8(r.IntN(256))
		}
		want := fromBottomUpRGBA(src, w, h)
		img := ring.slot(w, h)
		copy(img.Pix, src)
		hashes = ring.flipOpaqueInPlace(img, hash, hashes)
		if !bytes.Equal(img.Pix, want.Pix) {
			t.Fatalf("%dx%d: in-place flip differs", w, h)
		}
		if len(hashes) != h {
			t.Fatalf("%dx%d: %d row hashes", w, h, len(hashes))
		}
		for y := range h {
			if hashes[y] != hash(want.Pix[y*w*4:(y+1)*w*4]) {
				t.Fatalf("%dx%d: row %d hash differs", w, h, y)
			}
		}
		copy(img.Pix, src)
		if ring.flipOpaqueInPlace(img, nil, nil) != nil || !bytes.Equal(img.Pix, want.Pix) {
			t.Fatalf("%dx%d: unhashed flip differs", w, h)
		}
	}
}

func TestFrameRingSlotAdvancesOnlyOnDelivery(t *testing.T) {
	var ring frameRingBuffer
	a := ring.slot(4, 2)
	if again := ring.slot(4, 2); again != a {
		t.Fatal("slot must not advance before delivery")
	}
	ring.advance()
	if b := ring.slot(4, 2); b == a {
		t.Fatal("delivered slot reused by the next capture")
	}
}

// Old path: SDK staging buffer -> row copies -> alpha pass -> fingerprint pass.
// New path: SDK writes the slot -> one in-place flip+alpha+row-hash pass.
func BenchmarkCapturePostprocess1080p(b *testing.B) {
	const w, h = 1920, 1080
	seed := maphash.MakeSeed()
	hash := func(p []byte) uint64 { return maphash.Bytes(seed, p) }
	sdk := make([]byte, w*h*4)
	for i := range sdk {
		sdk[i] = uint8(i * 31)
	}
	b.Run("copy+alpha+hash", func(b *testing.B) {
		var ring frameRingBuffer
		b.SetBytes(int64(len(sdk)))
		for b.Loop() {
			img := fromBottomUpRGBAInto(ring.next(w, h), sdk)
			var sum uint64
			for y := range h {
				sum ^= hash(img.Pix[y*w*4 : (y+1)*w*4])
			}
		}
	})
	b.Run("inplace-fused", func(b *testing.B) {
		var ring frameRingBuffer
		var hashes []uint64
		b.SetBytes(int64(len(sdk)))
		for b.Loop() {
			// The SDK writes the slot itself (not timed, like the staging
			// buffer write of the old path).
			img := ring.slot(w, h)
			hashes = ring.flipOpaqueInPlace(img, hash, hashes)
			ring.advance()
		}
	})
}
