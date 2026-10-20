//go:build amd64 && !purego

package match

import "golang.org/x/sys/cpu"

// testKernels lists every SIMD kernel this CPU can execute. Run the tests
// under an emulator (qemu-x86_64 -cpu Haswell) to cover AVX2 on older hosts.
func testKernels() map[string]nccKernelFunc {
	out := map[string]nccKernelFunc{"sse2": nccSpansSSE2}
	if cpu.X86.HasAVX2 {
		out["avx2"] = nccSpansAVX2
	}
	return out
}
