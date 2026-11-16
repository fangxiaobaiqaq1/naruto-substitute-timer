//go:build windows

package diagnostics

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	performancePDHMoreData = uint32(0x800007d2)
	performancePDHNoData   = uint32(0x800007d5)
	performancePDHDouble   = 0x00000200
	performancePDHNoCap100 = 0x00008000
	performancePDHMaxBytes = 8 << 20
	performancePDHMaxItems = 32768
)

var (
	performancePDH        = windows.NewLazySystemDLL("pdh.dll")
	performancePDHOpen    = performancePDH.NewProc("PdhOpenQueryW")
	performancePDHAdd     = performancePDH.NewProc("PdhAddEnglishCounterW")
	performancePDHCollect = performancePDH.NewProc("PdhCollectQueryData")
	performancePDHArray   = performancePDH.NewProc("PdhGetFormattedCounterArrayW")
	performancePDHClose   = performancePDH.NewProc("PdhCloseQuery")
)

// PDH_FMT_COUNTERVALUE is a DWORD followed by an 8-byte-aligned union.
// Explicit padding is also necessary on 386, where Go aligns doubles to 4.
// PDH_FMT_COUNTERVALUE_ITEM_W is 24 bytes with FmtValue at offset 8 on all
// supported Windows architectures; do not use a packed 12-byte value struct.
type performancePDHValue struct {
	Status uint32
	_      uint32
	Double float64
}

type performancePDHItem struct {
	Name  uintptr
	_     [8 - unsafe.Sizeof(uintptr(0))]byte
	Value performancePDHValue
}

type performanceWindowsGPUCounter struct {
	handle      uintptr
	unavailable string
}

type performanceWindowsGPU struct {
	query                     uintptr
	engine, dedicated, shared performanceWindowsGPUCounter
	warm                      bool
	previous                  map[int]string // Only selected PIDs/StartIDs, bounded by the process cap.
	canceled                  func() bool    // Owner-only; checked between native calls, never kills one in flight.
}

func newPerformanceWindowsGPUWorker() *performanceGPUWorker {
	g := &performanceWindowsGPU{}
	return newPerformanceGPUWorker(performanceGPUNativeSlot, performanceGPUNativeOps{
		initialize: func(canceled func() bool) PerformanceSystem {
			g.canceled = canceled
			info := PerformanceSystem{Unavailable: make(map[string]string)}
			performanceWindowsGPUInventory(&info, canceled)
			if !canceled() {
				g.initialize()
			}
			return info
		},
		collect: func(s *performancePlatformSample, canceled func() bool) {
			g.canceled = canceled
			g.collect(s)
		},
		close: g.close,
	}, performanceGPUInitTimeout, performanceGPUReadTimeout)
}

// Initialization, collection, and cleanup are called ONLY by the slot owner.
func (g *performanceWindowsGPU) initialize() {
	for _, proc := range []*windows.LazyProc{performancePDHOpen, performancePDHAdd, performancePDHCollect, performancePDHArray, performancePDHClose} {
		if g.canceled() {
			return
		}
		if proc.Find() != nil {
			g.engine.unavailable = "PDH English GPU counter API unavailable"
			g.dedicated.unavailable, g.shared.unavailable = g.engine.unavailable, g.engine.unavailable
			return
		}
	}
	if g.canceled() {
		return
	}
	status, _, _ := performancePDHOpen.Call(0, 0, uintptr(unsafe.Pointer(&g.query)))
	if uint32(status) != 0 {
		g.engine.unavailable = performancePDHStatus("PdhOpenQuery", uint32(status))
		g.dedicated.unavailable, g.shared.unavailable = g.engine.unavailable, g.engine.unavailable
		g.close()
		return
	}
	g.engine = g.add(`\GPU Engine(*)\Utilization Percentage`)
	g.dedicated = g.add(`\GPU Process Memory(*)\Dedicated Usage`)
	g.shared = g.add(`\GPU Process Memory(*)\Shared Usage`)
}

func (g *performanceWindowsGPU) add(path string) performanceWindowsGPUCounter {
	var c performanceWindowsGPUCounter
	if g.canceled() {
		return c
	}
	name, _ := windows.UTF16PtrFromString(path) // Constant, local, English path; never a remote machine.
	status, _, _ := performancePDHAdd.Call(g.query, uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&c.handle)))
	if uint32(status) != 0 {
		c.handle = 0
		c.unavailable = performancePDHStatus("PdhAddEnglishCounter", uint32(status))
	}
	return c
}

func (g *performanceWindowsGPU) close() {
	if g.query != 0 {
		// Closing the query also frees all of its counter handles.
		performancePDHClose.Call(g.query)
		g.query = 0
	}
	g.warm = false
	g.previous = nil
}

func (g *performanceWindowsGPU) collect(s *performancePlatformSample) {
	if g.canceled() {
		return
	}
	selected := make(map[int]int, len(s.Processes))
	current := make(map[int]string, len(s.Processes))
	for i := range s.Processes {
		p := &s.Processes[i]
		selected[p.PID], current[p.PID] = i, p.StartID
	}
	queryReason := ""
	if g.query != 0 {
		status, _, _ := performancePDHCollect.Call(g.query)
		if uint32(status) != 0 {
			queryReason = performancePDHStatus("PdhCollectQueryData", uint32(status))
			g.warm = false
			g.previous = nil
		}
	} else {
		queryReason = "PDH query unavailable"
	}
	if g.canceled() {
		return
	}
	engineReason := g.engine.unavailable
	if engineReason == "" {
		engineReason = queryReason
	}
	if engineReason == "" && !g.warm {
		engineReason = "warm_up: GPU Engine rate requires two successful samples"
	}
	if engineReason != "" {
		performanceWindowsGPUUnavailable(s, "gpu_busiest_engine_percent", engineReason)
		performanceWindowsUnavailable(s.Unavailable, engineReason, "system_gpu_busiest_engine_percent", "tree_gpu_busiest_engine_percent", "gpu_engines")
	} else {
		g.collectEngines(s, selected, current)
	}
	if g.canceled() {
		return
	}
	g.collectMemory(s, selected, g.dedicated, "gpu_dedicated_bytes", queryReason)
	if g.canceled() {
		return
	}
	g.collectMemory(s, selected, g.shared, "gpu_shared_bytes", queryReason)
	if queryReason == "" {
		g.warm = true
		g.previous = current
	}
}

func performanceWindowsGPUUnavailable(s *performancePlatformSample, key, reason string) {
	s.Unavailable[key] = reason
	for i := range s.Processes {
		s.Processes[i].Unavailable[key] = reason
	}
}

func (g *performanceWindowsGPU) collectEngines(s *performancePlatformSample, selected map[int]int, current map[int]string) {
	var totals performanceGPUEngineTotals
	invalid := make(map[int]string)
	systemReason, treeReason := "", ""
	malformed := false
	reason := performanceWindowsPDHReadArray(g.engine.handle, g.canceled, func(name string, value float64, status uint32) {
		n, ok := parsePerformanceGPUInstance(name)
		if !ok || !n.hasEngine {
			malformed = true
			systemReason, treeReason = "unrecognised GPU Engine instance format", "unrecognised GPU Engine instance format"
			return
		}
		_, ours := selected[n.pid]
		if status > 1 || !performanceGPUValidNumber(value) {
			r := performancePDHStatus("GPU Engine instance", status)
			if status <= 1 {
				r = "GPU Engine returned a non-finite or negative value"
			}
			systemReason = r
			if ours {
				invalid[n.pid], treeReason = r, r
			}
			return
		}
		if ours && (current[n.pid] == "" || g.previous[n.pid] != current[n.pid]) {
			r := "warm_up: this process identity needs two GPU samples (PID reuse guarded)"
			invalid[n.pid], treeReason = r, r
			ours = false
		}
		// Group by LUID + physical-adapter index + engine index, NOT engine
		// type, PID or an all-engines sum. No unrelated PID rows are retained.
		totals.add(n, min(value, 100), ours)
	})
	if reason != "" {
		performanceWindowsGPUUnavailable(s, "gpu_busiest_engine_percent", reason)
		performanceWindowsUnavailable(s.Unavailable, reason, "system_gpu_busiest_engine_percent", "tree_gpu_busiest_engine_percent", "gpu_engines")
		return
	}
	performanceGPUApplyEngineTotals(s, &totals, invalid, systemReason, treeReason, malformed)
}

func (g *performanceWindowsGPU) collectMemory(s *performancePlatformSample, selected map[int]int, counter performanceWindowsGPUCounter, key, queryReason string) {
	reason := counter.unavailable
	if reason == "" {
		reason = queryReason
	}
	if reason != "" {
		performanceWindowsGPUUnavailable(s, key, reason)
		return
	}
	// Only running sums for the <=64 selected processes are retained, not a
	// system-wide map of GPU process-memory instances. No data is not zero.
	totals := make(map[int]float64)
	invalid := make(map[int]string)
	selectedCount := 0
	truncated, malformed := false, false
	reason = performanceWindowsPDHReadArray(counter.handle, g.canceled, func(name string, value float64, status uint32) {
		n, ok := parsePerformanceGPUInstance(name)
		if !ok || n.hasEngine {
			malformed = true
			return
		}
		if _, ours := selected[n.pid]; !ours {
			return
		}
		selectedCount++
		if selectedCount > performanceMaxGPUEngines {
			truncated = true
			return
		}
		if status > 1 || !performanceGPUValidNumber(value) {
			invalid[n.pid] = performancePDHStatus("GPU Process Memory instance", status)
			if status <= 1 {
				invalid[n.pid] = "GPU Process Memory returned a non-finite or negative value"
			}
			return
		}
		totals[n.pid] += value
	})
	if reason == "" && truncated {
		reason = "selected GPU process-memory instances limited to 256; incomplete totals omitted"
	}
	if reason == "" && malformed {
		reason = "unrecognised GPU Process Memory instance format"
	}
	if reason != "" {
		performanceWindowsGPUUnavailable(s, key, reason)
		return
	}
	if len(totals) == 0 {
		s.Unavailable[key] = "no matching GPU Process Memory data; driver/counter may be unavailable"
	}
	for pid, index := range selected {
		p := &s.Processes[index]
		if reason := invalid[pid]; reason != "" {
			p.Unavailable[key] = reason
		} else if value, ok := totals[pid]; ok && performanceGPUValidNumber(value) {
			p.Values[key] = value
		} else {
			p.Unavailable[key] = "no matching valid GPU Process Memory instance; absence is not measured zero"
		}
	}
}

// Read the transient native array, then immediately discard it. Buffers are
// aligned, limited to 8 MiB/32768 rows, and may grow at most three times under
// instance churn. Both the PDH return status and each row's CStatus are checked.
// Name pointers are validated against the buffer before dereferencing, with a
// bounded NUL search. No arbitrary pointer-to-string helper is used here.
func performanceWindowsPDHReadArray(counter uintptr, canceled func() bool, visit func(string, float64, uint32)) string {
	if canceled != nil && canceled() {
		return "GPU native operation canceled"
	}
	if counter == 0 {
		return "PDH counter unavailable"
	}
	var size, count uint32
	flags := uintptr(performancePDHDouble | performancePDHNoCap100)
	status, _, _ := performancePDHArray.Call(counter, flags, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if uint32(status) != performancePDHMoreData {
		if uint32(status) == 0 {
			return "PDH array has no instances"
		}
		return performancePDHStatus("PdhGetFormattedCounterArray(size)", uint32(status))
	}
	for attempt := 0; attempt < 3; attempt++ {
		if canceled != nil && canceled() {
			return "GPU native operation canceled"
		}
		if size == 0 || size > performancePDHMaxBytes || count > performancePDHMaxItems {
			return "PDH array exceeds buffer/item safety bound"
		}
		buffer := make([]uint64, (int(size)+7)/8) // C union/item alignment, including on 386.
		capacity := uint32(len(buffer) * 8)
		size = capacity
		count = 0
		status, _, _ = performancePDHArray.Call(counter, flags, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buffer[0])))
		if canceled != nil && canceled() {
			return "GPU native operation canceled"
		}
		if uint32(status) == performancePDHMoreData {
			continue
		}
		if uint32(status) != 0 {
			return performancePDHStatus("PdhGetFormattedCounterArray", uint32(status))
		}
		if size > capacity || count > performancePDHMaxItems || uint64(count)*uint64(unsafe.Sizeof(performancePDHItem{})) > uint64(size) {
			return "PDH array returned invalid buffer size/item count"
		}
		if count == 0 {
			return "PDH array has no instances"
		}
		reason := performanceWindowsPDHVisitBuffer(buffer, size, count, visit)
		runtime.KeepAlive(buffer)
		return reason
	}
	return "PDH instance list kept changing; bounded buffer retries exhausted"
}

func performanceWindowsPDHVisitBuffer(buffer []uint64, size, count uint32, visit func(string, float64, uint32)) string {
	itemBytes := uintptr(unsafe.Sizeof(performancePDHItem{})) * uintptr(count)
	if len(buffer) == 0 || size > performancePDHMaxBytes || uint64(size) > uint64(len(buffer))*8 || uint64(count) > performancePDHMaxItems || uint64(count)*uint64(unsafe.Sizeof(performancePDHItem{})) > uint64(size) {
		return "PDH array layout invalid"
	}
	base := unsafe.Pointer(&buffer[0])
	address := uintptr(base)
	items := unsafe.Slice((*performancePDHItem)(base), int(count))
	for _, item := range items {
		if item.Name < address || item.Name-address < itemBytes || item.Name-address >= uintptr(size) || (item.Name-address)%2 != 0 {
			return "PDH instance name pointer outside array buffer"
		}
		offset := item.Name - address
		units := min((uintptr(size)-offset)/2, 257)
		name := unsafe.Slice((*uint16)(unsafe.Add(base, offset)), int(units))
		terminated := false
		for i, c := range name {
			if c == 0 {
				visit(windows.UTF16ToString(name[:i]), item.Value.Double, item.Value.Status)
				terminated = true
				break
			}
		}
		if !terminated {
			return "PDH instance name exceeds bound or is not NUL-terminated"
		}
	}
	return ""
}

func performancePDHStatus(operation string, status uint32) string {
	reason := "counter data unavailable"
	switch status {
	case 0x800007d1, performancePDHNoData:
		reason = "no instances/data (GPU may be idle, absent or driver counters unavailable)"
	case 0xc0000bb8, 0xc0000bb9:
		reason = "GPU object/counter missing (requires a supporting WDDM driver)"
	case 5, 0xc0000bdb:
		reason = "access denied to performance counters"
	case 0xc0000bba, 0xc0000bc6, 0x800007d6, 0x800007d7, 0x800007d8:
		reason = "invalid data/rate (warm-up, instance churn or driver unavailable)"
	}
	return fmt.Sprintf("%s: %s (PDH 0x%08x)", operation, reason, status)
}

// DXGI enumerates present adapters, including adapters without an attached
// monitor. Only Description, LUID and the UMD version are retained; the other
// descriptor fields are discarded. Device paths and outputs are not queried.
type performanceDXGIAdapterDesc struct {
	Description                                                     [128]uint16
	VendorID, DeviceID, SubSysID, Revision                          uint32
	DedicatedVideoMemory, DedicatedSystemMemory, SharedSystemMemory uintptr
	LUIDLow                                                         uint32
	LUIDHigh                                                        int32
	Flags                                                           uint32
}

type performanceDXGIObject struct {
	VTable *[14]uintptr
}

func performanceWindowsGPUInventory(s *PerformanceSystem, canceled func() bool) {
	if canceled() {
		return
	}
	// Registry reads are restricted to display-class model/version values.
	// Matching these against DXGI descriptions supplies a driver fallback on
	// drivers that do not implement CheckInterfaceSupport's UMD version query.
	installed := make([]PerformanceGPU, 0)
	if class, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE); err == nil {
		names, _ := class.ReadSubKeyNames(128)
		if len(names) == 128 {
			s.Unavailable["gpu_driver_inventory"] = "display driver registry inventory limited to 128 entries"
		}
		for _, name := range names {
			if canceled() {
				break
			}
			if len(name) != 4 || strings.Trim(name, "0123456789") != "" {
				continue
			}
			key, err := registry.OpenKey(class, name, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			model := performanceWindowsRegistryString(key, "DriverDesc")
			driver := performanceWindowsRegistryString(key, "DriverVersion")
			key.Close()
			if model != "" {
				installed = append(installed, PerformanceGPU{Name: model, Driver: driver})
			}
		}
		class.Close()
	}
	if canceled() {
		return
	}
	factoryProc := windows.NewLazySystemDLL("dxgi.dll").NewProc("CreateDXGIFactory1")
	if factoryProc.Find() != nil {
		performanceWindowsGPUInventoryFallback(s, installed, "DXGI API unavailable")
		return
	}
	iidFactory := windows.GUID{Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba, Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}
	iidDevice := windows.GUID{Data1: 0x54ec77fa, Data2: 0x1377, Data3: 0x44e6, Data4: [8]byte{0x8c, 0x32, 0x88, 0xfd, 0x5f, 0x44, 0xc8, 0x4c}}
	var factory *performanceDXGIObject
	if canceled() {
		return
	}
	hr, _, _ := factoryProc.Call(uintptr(unsafe.Pointer(&iidFactory)), uintptr(unsafe.Pointer(&factory)))
	if int32(hr) < 0 || factory == nil {
		performanceWindowsGPUInventoryFallback(s, installed, "DXGI factory unavailable")
		return
	}
	defer syscall.SyscallN(factory.VTable[2], uintptr(unsafe.Pointer(factory))) // IUnknown::Release
	for index := 0; index < 32; index++ {
		if canceled() {
			return
		}
		var adapter *performanceDXGIObject
		hr, _, _ = syscall.SyscallN(factory.VTable[12], uintptr(unsafe.Pointer(factory)), uintptr(index), uintptr(unsafe.Pointer(&adapter))) // EnumAdapters1
		if uint32(hr) == 0x887a0002 {                                                                                                        // DXGI_ERROR_NOT_FOUND: normal end.
			break
		}
		if int32(hr) < 0 || adapter == nil {
			s.Unavailable["gpus"] = "DXGI adapter enumeration incomplete"
			break
		}
		if canceled() {
			syscall.SyscallN(adapter.VTable[2], uintptr(unsafe.Pointer(adapter)))
			return
		}
		var desc performanceDXGIAdapterDesc
		hr, _, _ = syscall.SyscallN(adapter.VTable[10], uintptr(unsafe.Pointer(adapter)), uintptr(unsafe.Pointer(&desc))) // GetDesc1
		if int32(hr) >= 0 && desc.Flags&2 == 0 {                                                                          // Exclude DXGI_ADAPTER_FLAG_SOFTWARE.
			gpu := PerformanceGPU{Name: strings.TrimSpace(windows.UTF16ToString(desc.Description[:])), Adapter: fmt.Sprintf("luid_0x%08x_0x%08x", uint32(desc.LUIDHigh), desc.LUIDLow)}
			if canceled() {
				syscall.SyscallN(adapter.VTable[2], uintptr(unsafe.Pointer(adapter)))
				return
			}
			var version uint64
			hr, _, _ = syscall.SyscallN(adapter.VTable[9], uintptr(unsafe.Pointer(adapter)), uintptr(unsafe.Pointer(&iidDevice)), uintptr(unsafe.Pointer(&version))) // CheckInterfaceSupport
			if int32(hr) >= 0 && version != 0 {
				gpu.Driver = fmt.Sprintf("%d.%d.%d.%d", version>>48, version>>32&0xffff, version>>16&0xffff, version&0xffff)
			} else {
				for _, entry := range installed {
					if entry.Name == gpu.Name && entry.Driver != "" {
						if gpu.Driver != "" && gpu.Driver != entry.Driver {
							gpu.Driver = "" // Ambiguous old/new driver registry records.
							break
						}
						gpu.Driver = entry.Driver
					}
				}
			}
			if gpu.Name == "" {
				s.Unavailable["gpu_name"] = "DXGI returned an empty adapter description"
			}
			if gpu.Driver == "" {
				s.Unavailable["gpu_driver"] = "driver version unavailable for one or more present adapters"
			}
			s.GPUs = append(s.GPUs, gpu)
		} else if int32(hr) < 0 {
			s.Unavailable["gpus"] = "DXGI adapter description unavailable"
		}
		syscall.SyscallN(adapter.VTable[2], uintptr(unsafe.Pointer(adapter)))
		if index == 31 {
			s.Unavailable["gpus"] = "DXGI adapter inventory limited to 32 adapters"
		}
	}
	if len(s.GPUs) == 0 {
		performanceWindowsGPUInventoryFallback(s, installed, "no hardware adapters available through DXGI")
	}
}

func performanceWindowsGPUInventoryFallback(s *PerformanceSystem, installed []PerformanceGPU, reason string) {
	s.GPUs = installed
	if len(installed) == 0 {
		s.Unavailable["gpus"] = reason + "; no readable display driver model/version records"
	} else {
		s.Unavailable["gpu_inventory"] = reason + "; installed-driver registry fallback may include disconnected adapters"
		for _, gpu := range installed {
			if gpu.Driver == "" {
				s.Unavailable["gpu_driver"] = "one or more installed display drivers have no readable version"
			}
		}
	}
}
