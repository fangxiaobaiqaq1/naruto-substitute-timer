//go:build !amd64 || purego

package match

// No SIMD kernel: Match uses the portable prefix-sum path (PreparedNCC.at).
var (
	nccKernel     nccKernelFunc
	nccKernelName = "go"
)
