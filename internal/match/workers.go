package match

import "runtime"

// Workers bounds recognition fan-out for n independent jobs. The emulator runs
// on the same machine and is the latency-critical producer, so analysis never
// takes more than half of the logical CPUs (and at most four) for one frame.
func Workers(n int) int {
	return max(1, min(n, runtime.GOMAXPROCS(0), runtime.NumCPU()/2, 4))
}
