//go:build windows

package diagnostics

import (
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// These fail at compile time as well, so an amd64/arm64 cross-compile checks
// the critical ABI sizes even on a host that cannot execute Windows tests.
const performanceTestPointerSize = unsafe.Sizeof(uintptr(0))

var (
	_ [24 - unsafe.Sizeof(performancePDHItem{})]byte
	_ [unsafe.Sizeof(performancePDHItem{}) - 24]byte
	_ [8 - unsafe.Offsetof(performancePDHItem{}.Value)]byte
	_ [unsafe.Offsetof(performancePDHItem{}.Value) - 8]byte
	_ [8 - unsafe.Offsetof(performancePDHValue{}.Double)]byte
	_ [unsafe.Offsetof(performancePDHValue{}.Double) - 8]byte
	_ [8 + 9*performanceTestPointerSize - unsafe.Sizeof(performanceWindowsMemoryCounters{})]byte
	_ [unsafe.Sizeof(performanceWindowsMemoryCounters{}) - (8 + 9*performanceTestPointerSize)]byte
	_ [11*performanceTestPointerSize - unsafe.Sizeof(performanceWindowsVMCounters{})]byte
	_ [unsafe.Sizeof(performanceWindowsVMCounters{}) - 11*performanceTestPointerSize]byte
	_ [8 + 12*performanceTestPointerSize - unsafe.Sizeof(performanceWindowsCommitInfo{})]byte
	_ [unsafe.Sizeof(performanceWindowsCommitInfo{}) - (8 + 12*performanceTestPointerSize)]byte
	_ [280 + 4*performanceTestPointerSize - unsafe.Sizeof(performanceDXGIAdapterDesc{})]byte
	_ [unsafe.Sizeof(performanceDXGIAdapterDesc{}) - (280 + 4*performanceTestPointerSize)]byte
)

func TestPerformanceWindowsNativeLayouts(t *testing.T) {
	wide := unsafe.Sizeof(uintptr(0)) == 8
	expect := func(narrow, wideSize uintptr) uintptr {
		if wide {
			return wideSize
		}
		return narrow
	}
	for _, tc := range []struct {
		name      string
		got, want uintptr
	}{
		{"MEMORYSTATUSEX", unsafe.Sizeof(performanceWindowsMemoryStatus{}), 64},
		{"PROCESS_MEMORY_COUNTERS_EX", unsafe.Sizeof(performanceWindowsMemoryCounters{}), expect(44, 80)},
		{"PrivateUsage", unsafe.Offsetof(performanceWindowsMemoryCounters{}.PrivateUsage), expect(40, 72)},
		{"VM_COUNTERS", unsafe.Sizeof(performanceWindowsVMCounters{}), expect(44, 88)},
		{"VirtualSize", unsafe.Offsetof(performanceWindowsVMCounters{}.VirtualSize), expect(4, 8)},
		{"PERFORMANCE_INFORMATION", unsafe.Sizeof(performanceWindowsCommitInfo{}), expect(56, 104)},
		{"CommitTotal", unsafe.Offsetof(performanceWindowsCommitInfo{}.CommitTotal), expect(4, 8)},
		{"CommitPageSize", unsafe.Offsetof(performanceWindowsCommitInfo{}.PageSize), expect(40, 80)},
		{"SYSTEM_INFO", unsafe.Sizeof(performanceWindowsSystemInfo{}), expect(36, 48)},
		{"PDH_FMT_COUNTERVALUE", unsafe.Sizeof(performancePDHValue{}), 16},
		{"PDH_DOUBLE", unsafe.Offsetof(performancePDHValue{}.Double), 8},
		{"PDH_FMT_COUNTERVALUE_ITEM_W", unsafe.Sizeof(performancePDHItem{}), 24},
		{"PDH_ITEM_VALUE", unsafe.Offsetof(performancePDHItem{}.Value), 8},
		{"DXGI_ADAPTER_DESC1", unsafe.Sizeof(performanceDXGIAdapterDesc{}), expect(296, 312)},
		{"DXGI_LUID", unsafe.Offsetof(performanceDXGIAdapterDesc{}.LUIDLow), expect(284, 296)},
		{"DXGI_FLAGS", unsafe.Offsetof(performanceDXGIAdapterDesc{}.Flags), expect(292, 304)},
		{"IO_COUNTERS", unsafe.Sizeof(windows.IO_COUNTERS{}), 48},
	} {
		if tc.got != tc.want {
			t.Errorf("%s layout on %s: got %d want %d", tc.name, runtime.GOARCH, tc.got, tc.want)
		}
	}
}

func TestPerformanceWindowsArchitectureAndErrors(t *testing.T) {
	for _, tc := range []struct {
		machine uint16
		arch    string
	}{
		{0x14c, "386"}, {0x8664, "amd64"}, {0x1c4, "arm"}, {0xaa64, "arm64"}, {0xa641, "arm64ec"}, {0xffff, ""},
	} {
		if got := performanceWindowsMachineArch(tc.machine); got != tc.arch {
			t.Errorf("machine %#x = %q, want %q", tc.machine, got, tc.arch)
		}
	}
	if performanceWindowsProcessorArch(12) != "arm64" {
		t.Fatal("native ARM64 architecture missing")
	}
	if got := performanceWindowsBasename(`C:\private\somebody\app.exe`); got != "app.exe" {
		t.Fatalf("basename retained a path: %q", got)
	}
	// Errors keep both the numeric code and the system's own message.
	if got := performanceWindowsError("OpenProcess", syscall.Errno(windows.ERROR_ACCESS_DENIED)); !strings.Contains(got, "OpenProcess") || !strings.Contains(got, "Win32 5") || len(got) < len("OpenProcess：(Win32 5)")+4 {
		t.Fatalf("Win32 error text = %q", got)
	}
	if got := performanceWindowsError("NtQuery", windows.STATUS_INFO_LENGTH_MISMATCH); !strings.Contains(got, "0xc0000004") {
		t.Fatalf("NTSTATUS text = %q", got)
	}
}

// This deliberately accepts explicit Unavailable GPU results: the same test
// must work on headless Windows CI without a WDDM driver. Run on real Windows
// to exercise actual PDH, DXGI, permissions and resource lifetimes.
func TestPerformanceWindowsCollectorSmoke(t *testing.T) {
	directory := t.TempDir()
	collector, err := newPerformanceCollector(os.Getpid(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Close()
	system := collector.System()
	if system.OS != "windows" || system.ProcessArch == "" || system.LogicalCPUs <= 0 {
		t.Fatalf("invalid system metadata: %+v", system)
	}
	var start string
	var cpu float64
	for i := 0; i < 2; i++ {
		s := collector.Collect()
		if len(s.Processes) == 0 || len(s.Processes) > 64 || len(s.GPUEngines) > 256 {
			t.Fatalf("invalid bounded process/GPU counts: %d / %d; unavailable=%v", len(s.Processes), len(s.GPUEngines), s.Unavailable)
		}
		// The system-wide list is what lets a report name the emulator: it must
		// contain other programs, with names, and our own process with real numbers.
		if len(s.AllProcesses) < 5 {
			t.Fatalf("system-wide process list has %d rows; unavailable=%v", len(s.AllProcesses), s.Unavailable)
		}
		self, named := false, 0
		for _, p := range s.AllProcesses {
			if p.Name != "" {
				named++
			}
			if p.PID == os.Getpid() {
				self = p.WorkingSet > 0 && p.Threads > 0 && p.StartID != "" && strings.HasSuffix(strings.ToLower(p.Name), ".exe")
			}
		}
		if !self || named < len(s.AllProcesses)/2 {
			t.Fatalf("system-wide rows: self=%v named=%d/%d", self, named, len(s.AllProcesses))
		}
		selected := make(map[int]bool)
		found := false
		for _, p := range s.Processes {
			selected[p.PID] = true
			if p.StartID == "" || p.CounterAt.IsZero() || p.Name == "" {
				t.Fatalf("invalid identity or missing name: %+v", p)
			}
			if p.PID == os.Getpid() {
				found = true
				v, ok := p.Values["cpu_seconds"]
				if !ok || i > 0 && (p.StartID != start || v < cpu) {
					t.Fatalf("invalid root CPU/identity: %+v", p)
				}
				start, cpu = p.StartID, v
			}
			for _, key := range []string{"cpu_seconds", "working_set_bytes", "private_bytes", "virtual_bytes", "page_faults", "threads", "handles", "gdi_objects", "user_objects", "io_read_bytes", "io_write_bytes", "gpu_busiest_engine_percent", "gpu_dedicated_bytes", "gpu_shared_bytes"} {
				if v, ok := p.Values[key]; ok {
					if !performanceGPUValidNumber(v) || p.Unavailable[key] != "" {
						t.Errorf("invalid/conflicting %s measurement: %v / %s", key, v, p.Unavailable[key])
					}
				} else if p.Unavailable[key] == "" {
					t.Errorf("missing %s without an explicit status", key)
				}
			}
		}
		if !found {
			t.Fatal("root was not collected")
		}
		for _, row := range s.GPUEngines {
			if !selected[row.PID] || row.Percent < 0 || row.Percent > 100 {
				t.Fatalf("unfiltered/invalid GPU row: %+v", row)
			}
		}
		for _, key := range []string{"system_cpu_total_seconds", "system_cpu_idle_seconds", "system_memory_total_bytes", "system_memory_available_bytes", "system_commit_used_bytes", "system_commit_limit_bytes", "disk_total_bytes", "disk_free_bytes", "system_gpu_busiest_engine_percent", "tree_gpu_busiest_engine_percent"} {
			if v, ok := s.Values[key]; ok {
				if !performanceGPUValidNumber(v) || s.Unavailable[key] != "" {
					t.Errorf("invalid/conflicting system %s: %v / %s", key, v, s.Unavailable[key])
				}
			} else if s.Unavailable[key] == "" {
				t.Errorf("missing system %s without status", key)
			}
		}
		t.Logf("sample %d: %d processes system-wide; GPU: %v / %s", i, len(s.AllProcesses), s.Values["system_gpu_busiest_engine_percent"], s.Unavailable["system_gpu_busiest_engine_percent"])
		if i == 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	collector.Close()
	collector.Close()
	if s := collector.Collect(); len(s.Values) != 0 || s.Unavailable["collector"] == "" {
		t.Fatal("closed collector should return status, not use released native handles")
	}
}

func TestPerformanceWindowsProcessorGroupCoverage(t *testing.T) {
	for _, cpus := range []int{65, 128, 256} {
		c := &performanceWindowsCollector{system: PerformanceSystem{LogicalCPUs: cpus}}
		s := performancePlatformSample{Values: make(map[string]float64), Unavailable: make(map[string]string)}
		c.collectSystem(&s)
		for _, key := range []string{"system_cpu_total_seconds", "system_cpu_idle_seconds"} {
			if _, ok := s.Values[key]; ok || !strings.Contains(s.Unavailable[key], "processor group") {
				t.Errorf("%d CPUs: partial-group %s must be omitted with a coverage explanation", cpus, key)
			}
		}
	}
	// Per-process cumulative seconds are not disabled on multi-group hosts;
	// they are normalised by core using System().LogicalCPUs across ALL groups.
	p, reason := performanceWindowsOpenProcess(os.Getpid())
	if p == nil {
		t.Fatal(reason)
	}
	defer windows.CloseHandle(p.handle)
	if _, ok := p.collect().Values["cpu_seconds"]; !ok {
		t.Fatal("process CPU counter unexpectedly unavailable")
	}
}

func TestPerformanceWindowsStartIDGuard(t *testing.T) {
	p, reason := performanceWindowsOpenProcess(os.Getpid())
	if p == nil {
		t.Fatal(reason)
	}
	defer windows.CloseHandle(p.handle)
	p.created++
	p.refreshTimes()
	if p.timesUnavailable == "" {
		t.Fatal("a different creation identity was accepted")
	}
}
