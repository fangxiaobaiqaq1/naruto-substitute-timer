package win

import (
	"image"
	"unsafe"
)

// bgraToRGBA copies the dst-sized client area at (ox, oy) of a top-down
// 32-bit BGRA DIB whose rows are ww pixels wide into dst (origin 0,0),
// swapping blue and red and forcing opaque alpha: exactly the pixels of the
// former per-pixel SetRGBA loop, one 32-bit word per pixel. The caller
// guarantees that the area lies inside src and that src and dst.Pix are
// 4-byte aligned (a DIB section and a Go-allocated Pix both are).
func bgraToRGBA(dst *image.RGBA, src []byte, ww, ox, oy int) {
	cw, ch := dst.Rect.Dx(), dst.Rect.Dy()
	if cw <= 0 || ch <= 0 {
		return
	}
	for y := 0; y < ch; y++ {
		s := src[((oy+y)*ww+ox)*4:][:cw*4]
		d := dst.Pix[y*dst.Stride:][:cw*4]
		in := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(s))), cw)
		out := unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(d))), cw)
		out = out[:len(in)]
		// Little-endian words: BGRA -> RGBA with A = 0xff.
		for i, w := range in {
			out[i] = (w>>16)&0xff | w&0xff00 | (w&0xff)<<16 | 0xff000000
		}
	}
}
