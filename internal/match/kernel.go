package match

// nccKernelFunc sums, over every span, img·tpl (dot), img (sum) and img²
// (sq). img points at the template's top-left pixel in the image (rows stride
// bytes apart) and the kernel never reads at or beyond img+avail; tpl is
// PreparedNCC.pix, consumed span after span. All three sums are exact
// integers, so any summation order gives the same values.
type nccKernelFunc func(img *byte, stride, avail int, spans *nccSpan, nspans int, tpl *byte) (dot, sum, sq uint64)

// nccSpansGo is the portable form of the kernel contract. It is the oracle of
// the assembly kernels' equivalence tests; Match itself uses the prefix-sum
// path (at) when no SIMD kernel is available.
func nccSpansGo(img []byte, stride int, spans []nccSpan, tpl []byte) (dot, sum, sq uint64) {
	for _, s := range spans {
		row := img[int(s.y)*stride+int(s.x):][:s.n]
		t := tpl[s.t : s.t+s.n]
		for i, v := range row {
			sum += uint64(v)
			sq += uint64(v) * uint64(v)
			dot += uint64(v) * uint64(t[i])
		}
	}
	return dot, sum, sq
}

// KernelName reports the NCC implementation Match uses on this machine:
// "avx2", "sse2" or "go" (diagnostics and benchmarks).
func KernelName() string { return nccKernelName }
