//go:build !linux && !windows

package diagnostics

import (
	"errors"
	"runtime"
)

// Runtime memory/GC/goroutine metrics are supplied by the common recorder. An
// unsupported platform must not prevent them from being recorded, or disguise
// absent native process/system counters as zero.
type otherPerformanceCollector struct{}

func newPerformanceCollector(rootPID int, directory string) (performanceCollector, error) {
	if rootPID <= 0 {
		return nil, errors.New("performance root PID must be positive")
	}
	return &otherPerformanceCollector{}, nil
}

func (*otherPerformanceCollector) System() PerformanceSystem {
	s := PerformanceSystem{
		OS: runtime.GOOS, ProcessArch: runtime.GOARCH, LogicalCPUs: runtime.NumCPU(),
		Unavailable: make(map[string]string),
	}
	for _, key := range []string{"os_version", "native_arch", "cpu_name", "total_ram_bytes", "gpus"} {
		s.Unavailable[key] = "unsupported: native system information is not implemented for this platform"
	}
	// GOARCH describes the process, not necessarily the native machine. The
	// empty native_arch/RAM/etc. fields are explicitly unavailable, not guesses.
	return s
}

func (*otherPerformanceCollector) Collect() performancePlatformSample {
	s := performancePlatformSample{
		Values: make(map[string]float64), Unavailable: make(map[string]string),
	}
	for _, key := range []string{
		"processes", "process_tree", "gpu", "gpu_engines",
		"system_cpu_total_seconds", "system_cpu_idle_seconds",
		"system_memory_total_bytes", "system_memory_available_bytes",
		"system_commit_used_bytes", "system_commit_limit_bytes",
		"system_swap_total_bytes", "system_swap_free_bytes",
		"disk_total_bytes", "disk_free_bytes",
		"system_gpu_busiest_engine_percent", "tree_gpu_busiest_engine_percent",
	} {
		s.Unavailable[key] = "unsupported: native performance collection is not implemented for this platform"
	}
	return s
}

func (*otherPerformanceCollector) Close() {}
