//go:build amd64 && !purego

package match

import "golang.org/x/sys/cpu"

// The span kernels live in kernel_amd64.s. SSE2 is part of the amd64
// baseline (GOAMD64=v1), so it needs no detection; AVX2 is chosen at run time
// only when both the CPU and the OS (saved YMM state) support it. Build with
// -tags purego to use the portable Go path instead.

//go:noescape
func nccSpansSSE2(img *byte, stride, avail int, spans *nccSpan, nspans int, tpl *byte) (dot, sum, sq uint64)

//go:noescape
func nccSpansAVX2(img *byte, stride, avail int, spans *nccSpan, nspans int, tpl *byte) (dot, sum, sq uint64)

//go:noescape
func grayRowsSSE2(dst *byte, dstStride int, src *byte, srcStride int, w4, h int)

var nccKernel, nccKernelName = pickKernel()

// grayRows converts RGBA rows to ToGray's luma, w4 (multiple of 4) pixels per
// row; nil means the portable loop only.
var grayRows = grayRowsSSE2

func pickKernel() (nccKernelFunc, string) {
	if cpu.X86.HasAVX2 {
		return nccSpansAVX2, "avx2"
	}
	return nccSpansSSE2, "sse2"
}
