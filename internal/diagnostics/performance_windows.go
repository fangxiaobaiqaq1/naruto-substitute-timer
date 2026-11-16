//go:build windows

package diagnostics

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const performanceWindowsMaxProcesses = 64

var (
	performanceKernel32     = windows.NewLazySystemDLL("kernel32.dll")
	performanceUser32       = windows.NewLazySystemDLL("user32.dll")
	performanceMemoryStatus = performanceKernel32.NewProc("GlobalMemoryStatusEx")
	performanceSystemTimes  = performanceKernel32.NewProc("GetSystemTimes")
	performanceNativeInfo   = performanceKernel32.NewProc("GetNativeSystemInfo")
	performanceMemoryInfo   = performanceKernel32.NewProc("K32GetProcessMemoryInfo")
	performanceCommitInfo   = performanceKernel32.NewProc("K32GetPerformanceInfo")
	performanceHandleCount  = performanceKernel32.NewProc("GetProcessHandleCount")
	performanceIOCounters   = performanceKernel32.NewProc("GetProcessIoCounters")
	performanceGUIResources = performanceUser32.NewProc("GetGuiResources")
	performanceSetLastError = performanceKernel32.NewProc("SetLastError")
)

// SIZE_T/pointers must remain pointer-sized. These layouts match the Windows
// SDK on both amd64 and arm64 (and the pointer-sized fields also work on 386).
type performanceWindowsMemoryStatus struct {
	Length, Load                                       uint32
	TotalPhys, AvailPhys, TotalPageFile, AvailPageFile uint64
	TotalVirtual, AvailVirtual, AvailExtendedVirtual   uint64
}

type performanceWindowsMemoryCounters struct {
	Size, PageFaultCount                               uint32
	PeakWorkingSetSize, WorkingSetSize                 uintptr
	QuotaPeakPagedPoolUsage, QuotaPagedPoolUsage       uintptr
	QuotaPeakNonPagedPoolUsage, QuotaNonPagedPoolUsage uintptr
	PagefileUsage, PeakPagefileUsage, PrivateUsage     uintptr
}

type performanceWindowsVMCounters struct {
	PeakVirtualSize, VirtualSize                       uintptr
	PageFaultCount                                     uint32
	PeakWorkingSetSize, WorkingSetSize                 uintptr
	QuotaPeakPagedPoolUsage, QuotaPagedPoolUsage       uintptr
	QuotaPeakNonPagedPoolUsage, QuotaNonPagedPoolUsage uintptr
	PagefileUsage, PeakPagefileUsage                   uintptr
}

type performanceWindowsCommitInfo struct {
	Size                                               uint32
	CommitTotal, CommitLimit, CommitPeak               uintptr
	PhysicalTotal, PhysicalAvailable, SystemCache      uintptr
	KernelTotal, KernelPaged, KernelNonpaged, PageSize uintptr
	HandleCount, ProcessCount, ThreadCount             uint32
}

type performanceWindowsSystemInfo struct {
	ProcessorArchitecture, Reserved                          uint16
	PageSize                                                 uint32
	MinimumApplicationAddress, MaximumApplicationAddress     uintptr
	ActiveProcessorMask                                      uintptr
	NumberOfProcessors, ProcessorType, AllocationGranularity uint32
	ProcessorLevel, ProcessorRevision                        uint16
}

type performanceWindowsProcess struct {
	handle                        windows.Handle // Pinned until removal/Close, never looked up again by PID.
	pid, parent                   int
	name                          string // Basename from Toolhelp, decoded only after tree membership checks.
	created, exited, kernel, user uint64
	timesUnavailable              string
	counterAt                     time.Time
}

type performanceWindowsCollector struct {
	mu              sync.Mutex
	rootPID         int
	rootUnavailable string
	volume          *uint16 // Verified local volume GUID root, never a report/executable path.
	diskBindStarted bool
	diskUnavailable string
	system          PerformanceSystem
	processes       map[int]*performanceWindowsProcess
	gpu             *performanceGPUWorker
	closed          bool
}

func newPerformanceCollector(rootPID int, _ string) (performanceCollector, error) {
	// The original report-directory string is intentionally never resolved or
	// queried. Core binds the already-open samples.jsonl handle instead.
	c := &performanceWindowsCollector{rootPID: rootPID, processes: make(map[int]*performanceWindowsProcess),
		diskUnavailable: "waiting for BindReportFile to confirm a local report volume"}
	if rootPID <= 0 || uint64(rootPID) > uint64(^uint32(0)) {
		c.rootUnavailable = "invalid root PID"
	} else if p, reason := performanceWindowsOpenProcess(rootPID); p == nil {
		// Do not retry an unpinned PID later: it may then belong to another app.
		c.rootUnavailable = reason
	} else {
		c.processes[rootPID] = p
	}
	c.system = c.readSystem()
	c.gpu = newPerformanceWindowsGPUWorker()
	// Individual failures belong in Unavailable; CPU/memory collection must
	// remain usable even without a GPU driver, registry access or a root handle.
	return c, nil
}

// BindReportFile is an optional core hook, called once after samples.jsonl has
// been opened. It neither owns nor closes the file. SyscallConn.Control pins the
// descriptor during lookup, so a concurrent File.Close cannot recycle it.
func (c *performanceWindowsCollector) BindReportFile(file *os.File) {
	c.mu.Lock()
	if c.closed || c.diskBindStarted {
		c.mu.Unlock()
		return
	}
	c.diskBindStarted = true
	c.mu.Unlock()
	var volume *uint16
	reason := "report file handle unavailable; local report volume not confirmed"
	if file != nil {
		if raw, err := file.SyscallConn(); err == nil {
			if err := raw.Control(func(fd uintptr) {
				volume, reason = performanceWindowsFileVolume(windows.Handle(fd))
			}); err != nil {
				volume, reason = nil, "report file handle closed or unavailable during volume binding"
			}
		}
	}
	c.mu.Lock()
	if !c.closed {
		c.volume, c.diskUnavailable = volume, reason
	}
	c.mu.Unlock()
}

func performanceWindowsFileVolume(file windows.Handle) (*uint16, string) {
	if kind, err := windows.GetFileType(file); err != nil || kind != windows.FILE_TYPE_DISK {
		return nil, "report handle is not a readable disk-file handle"
	}
	// FILE_NAME_OPENED avoids normalized-name traversal (including SMB parent
	// lookups); VOLUME_NAME_GUID refuses DOS/UNC aliases. No directory components
	// are queried, including after binding, so replacing a parent by a junction
	// cannot redirect later disk samples onto the network.
	const flags = 0x8 | 0x1 // FILE_NAME_OPENED | VOLUME_NAME_GUID
	buffer := make([]uint16, 512)
	for attempt := 0; attempt < 3; attempt++ {
		n, err := windows.GetFinalPathNameByHandle(file, &buffer[0], uint32(len(buffer)), flags)
		if err != nil {
			return nil, performanceWindowsError("GetFinalPathNameByHandle(volume GUID)", err)
		}
		if n >= uint32(len(buffer)) {
			if n >= 32768 {
				return nil, "report file volume lookup exceeded bounded native buffer"
			}
			buffer = make([]uint16, n+1)
			continue
		}
		if n < 49 || buffer[n] != 0 {
			return nil, "report handle did not resolve to a local volume GUID"
		}
		// Decode only the GUID root. Never retain/decode the private filename
		// suffix, and never include this internal volume identifier in a report.
		root, ok := performanceWindowsVolumeGUIDRoot(windows.UTF16ToString(buffer[:49]))
		if !ok {
			return nil, "report handle did not resolve to a local volume GUID"
		}
		volume, _ := windows.UTF16PtrFromString(root)
		switch windows.GetDriveType(volume) {
		case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE, windows.DRIVE_RAMDISK:
			return volume, ""
		default:
			return nil, "report handle volume is not confirmed as a local disk volume"
		}
	}
	return nil, "report file volume lookup changed repeatedly; disk query unavailable"
}

func performanceWindowsVolumeGUIDRoot(name string) (string, bool) {
	const prefix = `\\?\Volume{`
	if len(name) < 49 || !strings.EqualFold(name[:len(prefix)], prefix) || name[47:49] != `}\` {
		return "", false
	}
	for i, ch := range name[11:47] {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if ch != '-' {
				return "", false
			}
		} else if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
			return "", false
		}
	}
	return name[:49], true
}

func (c *performanceWindowsCollector) System() PerformanceSystem {
	s := c.system
	s.GPUs = append([]PerformanceGPU(nil), s.GPUs...)
	s.Unavailable = make(map[string]string, len(c.system.Unavailable))
	for k, v := range c.system.Unavailable {
		s.Unavailable[k] = v
	}
	// Base metadata is immutable; only merge the worker's cached GPU metadata.
	// Never acquire c.mu or call DXGI/PDH here, even while Collect/Close is active.
	info, reason := c.gpu.metadata()
	s.GPUs = info.GPUs
	for key, value := range info.Unavailable {
		s.Unavailable[key] = value
	}
	if reason != "" {
		s.Unavailable["gpu_sampling"] = reason
		if len(s.GPUs) == 0 {
			s.Unavailable["gpus"] = reason
		}
	}
	return s
}

func (c *performanceWindowsCollector) readSystem() PerformanceSystem {
	s := PerformanceSystem{OS: "windows", Unavailable: make(map[string]string)}
	v := windows.RtlGetVersion()
	if v.MajorVersion != 0 {
		s.OSVersion = fmt.Sprintf("%d.%d build %d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
		if v.MajorVersion == 10 {
			if v.BuildNumber >= 22000 && v.ProductType == 1 {
				s.OSVersion = "Windows 11 (" + s.OSVersion + ")"
			} else if v.ProductType == 1 {
				s.OSVersion = "Windows 10 (" + s.OSVersion + ")"
			}
		}
	} else {
		s.Unavailable["os_version"] = "RtlGetVersion returned no version"
	}
	if key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		if ubr, _, err := key.GetIntegerValue("UBR"); err == nil {
			s.OSVersion += fmt.Sprintf("; revision %d", ubr)
		}
		key.Close()
	}
	if key, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE); err == nil {
		s.CPUName = performanceWindowsRegistryString(key, "ProcessorNameString")
		key.Close()
	}
	if s.CPUName == "" {
		s.Unavailable["cpu_name"] = "CPU model registry value unavailable"
	}
	s.LogicalCPUs = int(windows.GetActiveProcessorCount(0xffff)) // ALL_PROCESSOR_GROUPS, not affinity/GOMAXPROCS.
	if s.LogicalCPUs == 0 {
		s.LogicalCPUs = runtime.NumCPU()
		s.Unavailable["logical_cpus"] = "GetActiveProcessorCount unavailable; using runtime-visible CPU count"
	}
	var native performanceWindowsSystemInfo
	if performanceNativeInfo.Find() == nil {
		performanceNativeInfo.Call(uintptr(unsafe.Pointer(&native)))
		s.NativeArch = performanceWindowsProcessorArch(native.ProcessorArchitecture)
	}
	if p := c.processes[c.rootPID]; p != nil {
		var processMachine, nativeMachine uint16
		if err := windows.IsWow64Process2(p.handle, &processMachine, &nativeMachine); err == nil {
			s.NativeArch = performanceWindowsMachineArch(nativeMachine)
			if processMachine == 0 { // IMAGE_FILE_MACHINE_UNKNOWN means not emulated.
				processMachine = nativeMachine
			}
			s.ProcessArch = performanceWindowsMachineArch(processMachine)
		}
	}
	if s.ProcessArch == "" {
		if c.rootPID == int(windows.GetCurrentProcessId()) {
			s.ProcessArch = runtime.GOARCH
		} else {
			s.Unavailable["process_arch"] = "IsWow64Process2/root process unavailable"
		}
	}
	if s.NativeArch == "" {
		s.Unavailable["native_arch"] = "native processor architecture unavailable"
	}
	var memory performanceWindowsMemoryStatus
	memory.Length = uint32(unsafe.Sizeof(memory))
	if reason := performanceWindowsBOOL(performanceMemoryStatus, uintptr(unsafe.Pointer(&memory))); reason == "" {
		s.TotalRAMBytes = memory.TotalPhys
	} else {
		s.Unavailable["total_ram_bytes"] = reason
	}
	return s
}

func performanceWindowsProcessorArch(arch uint16) string {
	switch arch {
	case 0:
		return "386"
	case 5:
		return "arm"
	case 9:
		return "amd64"
	case 12:
		return "arm64"
	}
	return ""
}

func performanceWindowsMachineArch(machine uint16) string {
	switch machine {
	case 0x14c:
		return "386"
	case 0x1c0, 0x1c4:
		return "arm"
	case 0x8664:
		return "amd64"
	case 0xaa64:
		return "arm64"
	case 0xa641:
		return "arm64ec"
	}
	return ""
}

// A small fixed buffer avoids unbounded registry allocation. Only allowlisted
// human-readable model/driver fields are read; never expand environment strings.
func performanceWindowsRegistryString(key registry.Key, name string) string {
	var buf [1024]uint16
	n, typ, err := key.GetValue(name, unsafe.Slice((*byte)(unsafe.Pointer(&buf[0])), len(buf)*2))
	if err != nil || typ != registry.SZ || n < 2 || n > len(buf)*2 || n%2 != 0 {
		return ""
	}
	return strings.TrimSpace(windows.UTF16ToString(buf[:n/2]))
}

func (c *performanceWindowsCollector) Collect() performancePlatformSample {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := performancePlatformSample{Values: make(map[string]float64), Unavailable: make(map[string]string)}
	if c.closed {
		s.Unavailable["collector"] = "collector closed"
		return s
	}
	c.collectSystem(&s)
	c.collectProcesses(&s)
	gpu, reason := c.gpu.sample(s.Processes)
	if reason != "" {
		for _, key := range []string{"gpu_busiest_engine_percent", "gpu_dedicated_bytes", "gpu_shared_bytes"} {
			performanceWindowsGPUUnavailable(&s, key, reason)
		}
		performanceWindowsUnavailable(s.Unavailable, reason, "system_gpu_busiest_engine_percent", "tree_gpu_busiest_engine_percent", "gpu_engines")
	} else {
		performanceGPUApplySample(&s, gpu)
	}
	// Keep the root handle even after exit so that the recorder cannot adopt a
	// reused root PID. Already observed, still-running orphans remain tracked.
	for pid, p := range c.processes {
		if pid != c.rootPID && p.exited != 0 {
			windows.CloseHandle(p.handle)
			delete(c.processes, pid)
		}
	}
	return s
}

func (c *performanceWindowsCollector) collectSystem(s *performancePlatformSample) {
	var idle, kernel, user windows.Filetime
	reason := ""
	if c.system.LogicalCPUs > 64 {
		// GetSystemTimes covers only the calling thread's processor group on
		// >64-CPU hosts. Do not label it as all-system CPU, and do not rely on
		// undocumented cross-group NtQuerySystemInformation behavior. Process
		// CPU seconds remain available; core normalises them using ALL groups.
		reason = "GetSystemTimes covers only the calling processor group on >64 logical CPUs; all-system CPU counters omitted"
	} else if c.system.Unavailable["logical_cpus"] != "" {
		reason = "all-system logical CPU count unavailable; cannot verify GetSystemTimes processor-group coverage"
	} else if reason = performanceWindowsBOOL(performanceSystemTimes, uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); reason == "" {
		s.Values["system_cpu_total_seconds"] = float64(performanceWindowsTicks(kernel)+performanceWindowsTicks(user)) / 1e7
		s.Values["system_cpu_idle_seconds"] = float64(performanceWindowsTicks(idle)) / 1e7
	}
	if reason != "" {
		performanceWindowsUnavailable(s.Unavailable, reason, "system_cpu_total_seconds", "system_cpu_idle_seconds")
	}
	var m performanceWindowsMemoryStatus
	m.Length = uint32(unsafe.Sizeof(m))
	if reason := performanceWindowsBOOL(performanceMemoryStatus, uintptr(unsafe.Pointer(&m))); reason == "" {
		s.Values["system_memory_total_bytes"] = float64(m.TotalPhys)
		s.Values["system_memory_available_bytes"] = float64(m.AvailPhys)
	} else {
		performanceWindowsUnavailable(s.Unavailable, reason, "system_memory_total_bytes", "system_memory_available_bytes")
	}
	var commit performanceWindowsCommitInfo
	commit.Size = uint32(unsafe.Sizeof(commit))
	if reason := performanceWindowsBOOL(performanceCommitInfo, uintptr(unsafe.Pointer(&commit)), uintptr(commit.Size)); reason != "" {
		performanceWindowsUnavailable(s.Unavailable, reason, "system_commit_used_bytes", "system_commit_limit_bytes")
	} else if commit.PageSize == 0 || commit.CommitTotal > commit.CommitLimit {
		performanceWindowsUnavailable(s.Unavailable, "GetPerformanceInfo returned invalid commit counters", "system_commit_used_bytes", "system_commit_limit_bytes")
	} else {
		s.Values["system_commit_used_bytes"] = float64(commit.CommitTotal) * float64(commit.PageSize)
		s.Values["system_commit_limit_bytes"] = float64(commit.CommitLimit) * float64(commit.PageSize)
	}
	// GlobalMemoryStatusEx's *PageFile fields describe commit, NOT swap size.
	// Do not mislabel them as system_swap_*.
	if c.diskUnavailable != "" || c.volume == nil {
		reason := c.diskUnavailable
		if reason == "" {
			reason = "report file has not been bound to a local volume"
		}
		performanceWindowsUnavailable(s.Unavailable, reason, "disk_total_bytes", "disk_free_bytes")
	} else {
		var available, total, free uint64
		if err := windows.GetDiskFreeSpaceEx(c.volume, &available, &total, &free); err != nil {
			performanceWindowsUnavailable(s.Unavailable, performanceWindowsError("GetDiskFreeSpaceEx", err), "disk_total_bytes", "disk_free_bytes")
		} else {
			s.Values["disk_total_bytes"] = float64(total)
			s.Values["disk_free_bytes"] = float64(available) // Caller/quota-available space.
		}
	}
}

func performanceWindowsTicks(t windows.Filetime) uint64 {
	return uint64(t.HighDateTime)<<32 | uint64(t.LowDateTime)
}

func performanceWindowsOpenProcess(pid int) (*performanceWindowsProcess, string) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		h, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	}
	if err != nil {
		return nil, performanceWindowsError("OpenProcess", err)
	}
	p := &performanceWindowsProcess{pid: pid, handle: h}
	p.refreshTimes()
	if p.timesUnavailable != "" {
		windows.CloseHandle(h)
		return nil, p.timesUnavailable
	}
	return p, ""
}

func (p *performanceWindowsProcess) refreshTimes() {
	var created, exited, kernel, user windows.Filetime
	p.timesUnavailable = ""
	err := windows.GetProcessTimes(p.handle, &created, &exited, &kernel, &user)
	p.counterAt = time.Now()
	if err != nil {
		p.timesUnavailable = performanceWindowsError("GetProcessTimes", err)
		return
	}
	start := performanceWindowsTicks(created)
	if start == 0 || p.created != 0 && start != p.created {
		p.timesUnavailable = "process creation identity changed or unavailable"
		return
	}
	p.created, p.exited = start, performanceWindowsTicks(exited)
	p.kernel, p.user = performanceWindowsTicks(kernel), performanceWindowsTicks(user)
}

type performanceWindowsSnapshotEntry struct {
	pid, parent int
	threads     uint32
}

// The Toolhelp API supplies names as part of its fixed structure. Unrelated
// ExeFile fields are never decoded/copied into Go strings or retained. The
// discovery index stores only numeric ancestry/thread data, with a hard bound.
func performanceWindowsWalkSnapshot(h windows.Handle, visit func(*windows.ProcessEntry32) bool) string {
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	err := windows.Process32First(h, &entry)
	for err == nil {
		if !visit(&entry) {
			return "process snapshot exceeded 32768-entry safety bound"
		}
		err = windows.Process32Next(h, &entry)
	}
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return ""
	}
	return performanceWindowsError("Toolhelp process snapshot", err)
}

func (c *performanceWindowsCollector) collectProcesses(s *performancePlatformSample) {
	if c.rootUnavailable != "" {
		s.Unavailable["processes"] = c.rootUnavailable + "; root identity was not pinned, collection will not adopt a later PID reuse"
		return
	}
	for _, p := range c.processes {
		p.refreshTimes()
		if p.timesUnavailable != "" {
			s.Unavailable["processes"] = "one or more observed process identities could not be revalidated"
		}
	}
	entries := make(map[int]performanceWindowsSnapshotEntry)
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	snapshotReason := ""
	if err != nil {
		snapshotReason = performanceWindowsError("CreateToolhelp32Snapshot", err)
	} else {
		defer windows.CloseHandle(snapshot)
		snapshotReason = performanceWindowsWalkSnapshot(snapshot, func(e *windows.ProcessEntry32) bool {
			if len(entries) >= 32768 {
				return false
			}
			pid := int(e.ProcessID)
			entries[pid] = performanceWindowsSnapshotEntry{pid, int(e.ParentProcessID), e.Threads}
			return true
		})
	}
	if snapshotReason != "" {
		s.Unavailable["process_discovery"] = snapshotReason
	} else {
		c.discoverChildren(entries, s)
		// A second pass over the SAME snapshot decodes names only for admitted
		// identities. Held handles pin PIDs throughout this pass and GPU sampling.
		if reason := performanceWindowsWalkSnapshot(snapshot, func(e *windows.ProcessEntry32) bool {
			if p := c.processes[int(e.ProcessID)]; p != nil && p.timesUnavailable == "" {
				p.name = performanceWindowsBasename(windows.UTF16ToString(e.ExeFile[:]))
				if p.pid == c.rootPID {
					p.parent = int(e.ParentProcessID)
				}
			}
			return true
		}); reason != "" {
			s.Unavailable["process_names"] = reason
		}
	}
	ids := make([]int, 0, len(c.processes))
	for pid := range c.processes {
		ids = append(ids, pid)
	}
	sort.Ints(ids)
	// Root first is useful to callers, but no consumer should infer root by index.
	for i, pid := range ids {
		if pid == c.rootPID {
			copy(ids[1:i+1], ids[:i])
			ids[0] = pid
			break
		}
	}
	for _, pid := range ids {
		p := c.processes[pid]
		if p.timesUnavailable != "" || p.exited != 0 {
			if pid == c.rootPID && p.exited != 0 {
				s.Unavailable["root_process"] = "root process exited; its PID will not be adopted again"
			}
			continue
		}
		process := p.collect()
		if e, ok := entries[pid]; ok && snapshotReason == "" {
			process.Values["threads"] = float64(e.threads)
		} else {
			process.Unavailable["threads"] = "process not present in a complete Toolhelp snapshot"
		}
		s.Processes = append(s.Processes, process)
	}
}

func (c *performanceWindowsCollector) discoverChildren(entries map[int]performanceWindowsSnapshotEntry, s *performancePlatformSample) {
	children := make(map[int][]int)
	for pid, e := range entries {
		children[e.parent] = append(children[e.parent], pid)
	}
	queue := make([]int, 0, performanceWindowsMaxProcesses)
	for pid := range c.processes {
		queue = append(queue, pid)
	}
	sort.Ints(queue)
	for i := 0; i < len(queue); i++ {
		parent := c.processes[queue[i]]
		if parent.timesUnavailable != "" {
			continue
		}
		ids := children[parent.pid]
		sort.Ints(ids)
		for _, pid := range ids {
			if _, known := c.processes[pid]; known || pid <= 0 {
				continue
			}
			if len(c.processes) >= performanceWindowsMaxProcesses {
				s.Unavailable["process_tree"] = "observed process tree limited to 64 pinned identities; unobserved descendants omitted"
				return
			}
			child, reason := performanceWindowsOpenProcess(pid)
			if child == nil {
				s.Unavailable["process_discovery"] = "a candidate descendant could not be identity-checked: " + reason
				continue
			}
			var basic windows.PROCESS_BASIC_INFORMATION
			var length uint32
			err := windows.NtQueryInformationProcess(child.handle, windows.ProcessBasicInformation, unsafe.Pointer(&basic), uint32(unsafe.Sizeof(basic)), &length)
			// Creation bounds stop a recycled parent's PID from claiming unrelated
			// children. The native PPID also rejects snapshot/OpenProcess races.
			if err != nil || length != uint32(unsafe.Sizeof(basic)) {
				s.Unavailable["process_discovery"] = "a candidate descendant's native parent identity was unavailable"
				windows.CloseHandle(child.handle)
				continue
			}
			if int(basic.InheritedFromUniqueProcessId) != parent.pid || child.created < parent.created || parent.exited != 0 && child.created > parent.exited || child.exited != 0 {
				windows.CloseHandle(child.handle)
				continue
			}
			child.parent = parent.pid
			c.processes[pid] = child
			queue = append(queue, pid)
		}
	}
}

func performanceWindowsBasename(name string) string {
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	return name
}

func (p *performanceWindowsProcess) collect() PerformanceProcess {
	// Discovery can take a variable amount of time. Re-read adjacent CPU/IO
	// counters here rather than using the identity check before the snapshot.
	p.refreshTimes()
	s := PerformanceProcess{PID: p.pid, ParentPID: p.parent, Name: p.name,
		StartID: strconv.FormatUint(p.created, 10), CounterAt: p.counterAt, Values: make(map[string]float64), Unavailable: make(map[string]string)}
	if s.Name == "" {
		s.Unavailable["name"] = "process basename unavailable in snapshot"
	}
	if p.timesUnavailable == "" {
		s.Values["cpu_seconds"] = (float64(p.kernel) + float64(p.user)) / 1e7
	} else {
		s.Unavailable["cpu_seconds"] = p.timesUnavailable
	}
	var io windows.IO_COUNTERS
	if reason := performanceWindowsBOOL(performanceIOCounters, uintptr(p.handle), uintptr(unsafe.Pointer(&io))); reason != "" {
		performanceWindowsUnavailable(s.Unavailable, reason, "io_read_bytes", "io_write_bytes")
	} else {
		s.Values["io_read_bytes"] = float64(io.ReadTransferCount)
		s.Values["io_write_bytes"] = float64(io.WriteTransferCount)
	}
	var memory performanceWindowsMemoryCounters
	memory.Size = uint32(unsafe.Sizeof(memory))
	if reason := performanceWindowsBOOL(performanceMemoryInfo, uintptr(p.handle), uintptr(unsafe.Pointer(&memory)), uintptr(memory.Size)); reason != "" {
		performanceWindowsUnavailable(s.Unavailable, reason, "working_set_bytes", "peak_working_set_bytes", "private_bytes", "page_faults")
	} else {
		s.Values["working_set_bytes"] = float64(memory.WorkingSetSize)
		s.Values["peak_working_set_bytes"] = float64(memory.PeakWorkingSetSize)
		s.Values["private_bytes"] = float64(memory.PrivateUsage)
		s.Values["page_faults"] = float64(memory.PageFaultCount)
	}
	var vm performanceWindowsVMCounters
	var length uint32
	if err := windows.NtQueryInformationProcess(p.handle, windows.ProcessVmCounters, unsafe.Pointer(&vm), uint32(unsafe.Sizeof(vm)), &length); err != nil {
		s.Unavailable["virtual_bytes"] = performanceWindowsError("NtQueryInformationProcess(VM)", err)
	} else if length < uint32(unsafe.Offsetof(vm.VirtualSize)+unsafe.Sizeof(vm.VirtualSize)) || length > uint32(unsafe.Sizeof(vm)) {
		s.Unavailable["virtual_bytes"] = "invalid native VM counter length"
	} else {
		s.Values["virtual_bytes"] = float64(vm.VirtualSize)
	}
	var handles uint32
	if reason := performanceWindowsBOOL(performanceHandleCount, uintptr(p.handle), uintptr(unsafe.Pointer(&handles))); reason != "" {
		s.Unavailable["handles"] = reason
	} else {
		s.Values["handles"] = float64(handles)
	}
	for flag, key := range []string{"gdi_objects", "user_objects"} {
		if value, reason := performanceWindowsGUIObjects(p.handle, uintptr(flag)); reason != "" {
			s.Unavailable[key] = reason
		} else {
			s.Values[key] = float64(value)
		}
	}
	return s
}

func performanceWindowsGUIObjects(h windows.Handle, flag uintptr) (uint32, string) {
	if performanceGUIResources.Find() != nil || performanceSetLastError.Find() != nil {
		return 0, "GetGuiResources API unavailable"
	}
	// Zero is legitimate for a non-GUI process. Clear/read LastError on the
	// same OS thread to distinguish this from access denied/API failure.
	runtime.LockOSThread()
	performanceSetLastError.Call(0)
	r, _, err := performanceGUIResources.Call(uintptr(h), flag)
	runtime.UnlockOSThread()
	if r == 0 && err != syscall.Errno(0) {
		return 0, performanceWindowsError("GetGuiResources", err)
	}
	return uint32(r), ""
}

//go:uintptrescapes
func performanceWindowsBOOL(proc *windows.LazyProc, args ...uintptr) string {
	if proc.Find() != nil {
		return proc.Name + ": API unavailable"
	}
	r, _, err := proc.Call(args...)
	if r == 0 {
		return performanceWindowsError(proc.Name, err)
	}
	return ""
}

func performanceWindowsError(operation string, err error) string {
	// Never include error strings containing a directory, device path or DLL
	// search path. Numeric native errors retain actionable permission/status info.
	var errno syscall.Errno
	if errors.As(err, &errno) && errno != 0 {
		if errno == windows.ERROR_ACCESS_DENIED {
			return operation + ": access denied"
		}
		return fmt.Sprintf("%s unavailable (Win32 %d)", operation, uint32(errno))
	}
	var status windows.NTStatus
	if errors.As(err, &status) {
		return fmt.Sprintf("%s unavailable (NTSTATUS 0x%08x)", operation, uint32(status))
	}
	return operation + ": unavailable"
}

func performanceWindowsUnavailable(m map[string]string, reason string, keys ...string) {
	for _, key := range keys {
		m[key] = reason
	}
}

func (c *performanceWindowsCollector) Close() {
	// Cancel a bounded GPU wait before taking the CPU/process lock. The native
	// owner can stay blocked indefinitely without delaying this stop request.
	c.gpu.close()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for pid, p := range c.processes {
		windows.CloseHandle(p.handle)
		delete(c.processes, pid)
	}
}
