//go:build windows

package diagnostics

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"time"
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
	query                                                      uintptr
	engine, dedicated, shared, adapterDedicated, adapterShared performanceWindowsGPUCounter
	warm                                                       bool
	canceled                                                   func() bool // Worker-only; checked between native calls.
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
		collect: func(canceled func() bool) *performanceGPUSample {
			g.canceled = canceled
			return g.collect()
		},
		close: g.close,
	}, PerformanceInterval)
}

// Initialization, collection, and cleanup are called ONLY by the worker.
func (g *performanceWindowsGPU) initialize() {
	all := func(reason string) {
		for _, c := range []*performanceWindowsGPUCounter{&g.engine, &g.dedicated, &g.shared, &g.adapterDedicated, &g.adapterShared} {
			c.unavailable = reason
		}
	}
	for _, proc := range []*windows.LazyProc{performancePDHOpen, performancePDHAdd, performancePDHCollect, performancePDHArray, performancePDHClose} {
		if err := proc.Find(); err != nil {
			all("PDH GPU 计数器接口不可用：" + err.Error())
			return
		}
	}
	if g.canceled() {
		return
	}
	status, _, _ := performancePDHOpen.Call(0, 0, uintptr(unsafe.Pointer(&g.query)))
	if uint32(status) != 0 {
		all(performancePDHStatus("PdhOpenQuery", uint32(status)))
		g.close()
		return
	}
	g.engine = g.add(`\GPU Engine(*)\Utilization Percentage`)
	g.dedicated = g.add(`\GPU Process Memory(*)\Dedicated Usage`)
	g.shared = g.add(`\GPU Process Memory(*)\Shared Usage`)
	g.adapterDedicated = g.add(`\GPU Adapter Memory(*)\Dedicated Usage`)
	g.adapterShared = g.add(`\GPU Adapter Memory(*)\Shared Usage`)
}

func (g *performanceWindowsGPU) add(path string) performanceWindowsGPUCounter {
	var c performanceWindowsGPUCounter
	if g.canceled() {
		c.unavailable = "GPU 采集已停止"
		return c
	}
	// PdhAddEnglishCounter: the English path works on localized (Chinese) Windows.
	name, _ := windows.UTF16PtrFromString(path)
	status, _, _ := performancePDHAdd.Call(g.query, uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&c.handle)))
	if uint32(status) != 0 {
		c.handle = 0
		c.unavailable = performancePDHStatus("PdhAddEnglishCounter "+path, uint32(status))
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
}

// collect returns nil only when canceled mid-way. Every counter fails or
// succeeds on its own; a bad row is skipped inside the sample.
func (g *performanceWindowsGPU) collect() *performanceGPUSample {
	s := newPerformanceGPUSample()
	if g.query == 0 {
		reason := g.engine.unavailable
		if reason == "" {
			reason = "PDH 查询不可用"
		}
		s.engineReason, s.memoryReason, s.adapterReason = reason, reason, reason
		return s
	}
	if status, _, _ := performancePDHCollect.Call(g.query); uint32(status) != 0 {
		reason := performancePDHStatus("PdhCollectQueryData", uint32(status))
		s.engineReason, s.memoryReason, s.adapterReason = reason, reason, reason
		g.warm = false
		return s
	}
	read := func(c performanceWindowsGPUCounter, visit func(string, float64, uint32)) string {
		if c.unavailable != "" {
			return c.unavailable
		}
		reason := performanceWindowsPDHReadArray(c.handle, g.canceled, visit)
		if reason == performancePDHNoInstances {
			return "" // No process holds a GPU context right now: a real zero.
		}
		return reason
	}
	// Utilization is a rate: PDH needs two collections before it has a value.
	if !g.warm {
		s.engineReason = "GPU 占用率需要连续两次采样，等待下一秒"
	} else {
		s.engineReason = read(g.engine, s.addEngine)
	}
	memory := func(c performanceWindowsGPUCounter, shared bool) string {
		return read(c, func(name string, value float64, status uint32) { s.addMemory(name, value, status, shared) })
	}
	s.memoryReason = memory(g.dedicated, false)
	if reason := memory(g.shared, true); s.memoryReason == "" {
		s.memoryReason = reason
	}
	s.adapterReason = memory(g.adapterDedicated, false)
	if reason := memory(g.adapterShared, true); s.adapterReason == "" {
		s.adapterReason = reason
	}
	g.warm = true
	if g.canceled() {
		return nil
	}
	s.at = time.Now()
	return s
}

const performancePDHNoInstances = "PDH array has no instances"

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
			return performancePDHNoInstances
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
			return performancePDHNoInstances
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
	reason := "计数器数据不可用"
	switch status {
	case 0x800007d1, performancePDHNoData:
		reason = "没有实例/数据（GPU 空闲、不存在或驱动不提供计数器）"
	case 0xc0000bb8, 0xc0000bb9:
		reason = "系统没有该 GPU 计数器（需要 Windows 10 1709+ 与 WDDM 2.x 驱动）"
	case 5, 0xc0000bdb:
		reason = "没有读取性能计数器的权限"
	case 0xc0000bba, 0xc0000bc6, 0x800007d6, 0x800007d7, 0x800007d8:
		reason = "数据无效（预热中、实例变动或驱动不可用）"
	}
	return fmt.Sprintf("%s：%s (PDH 0x%08x)", operation, reason, status)
}

// DXGI enumerates present adapters, including adapters without an attached
// monitor: name, LUID, driver version and dedicated/shared memory sizes.
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
			gpu := PerformanceGPU{Name: strings.TrimSpace(windows.UTF16ToString(desc.Description[:])), Adapter: fmt.Sprintf("luid_0x%08x_0x%08x", uint32(desc.LUIDHigh), desc.LUIDLow),
				DedicatedBytes: uint64(desc.DedicatedVideoMemory), SharedBytes: uint64(desc.SharedSystemMemory)}
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
