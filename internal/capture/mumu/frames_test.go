package mumu

import "testing"

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
