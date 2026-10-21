package match

import (
	"runtime"
	"testing"
)

func TestWorkersLeavesCPUsForEmulator(t *testing.T) {
	for _, n := range []int{0, 1, 2, 7, 100} {
		got := Workers(n)
		if got < 1 || got > 4 || (n > 0 && got > n) || (runtime.NumCPU() >= 2 && got > runtime.NumCPU()/2) {
			t.Fatalf("Workers(%d) = %d on %d CPUs", n, got, runtime.NumCPU())
		}
	}
}
