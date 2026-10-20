//go:build linux

package match

import (
	"fmt"
	"math/rand/v2"
	"syscall"
	"testing"
)

// guardedBuffer returns n bytes that end exactly at a PROT_NONE page: any
// kernel read past the image (tails use 16-byte loads) faults the test.
func guardedBuffer(t *testing.T, n int) []byte {
	t.Helper()
	page := syscall.Getpagesize()
	size := (n+page-1)/page*page + page
	mem, err := syscall.Mmap(-1, 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		t.Skip("mmap:", err)
	}
	t.Cleanup(func() { syscall.Munmap(mem) })
	if err := syscall.Mprotect(mem[size-page:], syscall.PROT_NONE); err != nil {
		t.Skip("mprotect:", err)
	}
	return mem[size-page-n : size-page : size-page]
}

func TestKernelsNeverReadPastImage(t *testing.T) {
	kernelNames(t)
	r := rand.New(rand.NewPCG(1, 99))
	for n := 1; n <= 80; n++ {
		templ := randomGray(r, n, 3)
		p := PrepareNCC(templ, diamondMask(n, 3))
		full := PrepareNCC(templ, nil)
		for _, stride := range []int{n, n + 7, n + 16} {
			for x := 0; x+n <= stride; x++ {
				// The window's last pixel is the buffer's last byte.
				pix := guardedBuffer(t, 2*stride+x+n)
				for i := range pix {
					pix[i] = uint8(r.IntN(256))
				}
				checkKernels(t, fmt.Sprintf("guard n=%d stride=%d", n, stride), pix, stride, x, full)
				checkKernels(t, fmt.Sprintf("guard diamond n=%d stride=%d", n, stride), pix, stride, x, p)
			}
		}
	}
}
