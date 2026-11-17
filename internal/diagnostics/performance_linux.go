//go:build linux

package diagnostics

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	linuxPerformanceMaxProcesses = 64
	linuxPerformanceDirBatch     = 64
	linuxPerformanceMaxFDs       = 1024
	linuxPerformanceMaxTasks     = 256
	linuxPerformanceMaxScanItems = 4096 // Directory names AND task/children PID tokens, across the whole sample.
	linuxPerformanceFDTime       = 2 * time.Millisecond
	linuxPerformanceTaskTime     = 4 * time.Millisecond
	linuxPerformanceCollectTime  = 50 * time.Millisecond
	linuxPerformanceChildrenSize = 8 << 10
	linuxPerformanceInvalid      = "unavailable: missing or invalid procfs counter"
	linuxPerformanceGPU          = "unsupported: Linux has no stable generic GPU accounting API"
)

var (
	errLinuxPerformanceData       = errors.New("invalid procfs data")
	errLinuxPerformanceScanBudget = errors.New("collection scan budget exhausted")
	errLinuxPerformanceDirLimit   = errors.New("directory entry budget exhausted")
	errLinuxPerformanceDirTime    = errors.New("directory time budget exhausted")
	errLinuxPerformanceTreeLimit  = errors.New("observed process tree limited to 64 processes")
)

var linuxPerformanceMemoryKeys = map[string]string{
	"MemTotal":     "system_memory_total_bytes",
	"MemAvailable": "system_memory_available_bytes",
	"Committed_AS": "system_commit_used_bytes",
	"CommitLimit":  "system_commit_limit_bytes",
	"SwapTotal":    "system_swap_total_bytes",
	"SwapFree":     "system_swap_free_bytes",
}

type linuxPerformanceCollector struct {
	rootPID          int
	rootStartID      string
	rootUnavailable  string
	procDirectory    string
	directory        string
	clockTicks       uint64
	clockUnavailable string
	pageSize         uint64
	system           PerformanceSystem

	// Optional per-collector test seams. Nil uses local OS APIs and the real
	// monotonic clock. CounterAt always uses time.Now, never this budget clock.
	openDir func(string) (linuxPerformanceDirectory, error)
	scanNow func() time.Time
}

type linuxPerformanceDirectory interface {
	Readdirnames(int) ([]string, error)
	Close() error
}

type linuxPerformanceScanBudget struct {
	now       func() time.Time
	deadline  time.Time
	remaining int
}

func newLinuxPerformanceScanBudget(now func() time.Time) *linuxPerformanceScanBudget {
	if now == nil {
		now = time.Now
	}
	return &linuxPerformanceScanBudget{
		now: now, deadline: now().Add(linuxPerformanceCollectTime), remaining: linuxPerformanceMaxScanItems,
	}
}

func (b *linuxPerformanceScanBudget) check(until time.Time) error {
	now := b.now()
	if b.remaining <= 0 || !now.Before(b.deadline) {
		return errLinuxPerformanceScanBudget
	}
	if !until.IsZero() && !now.Before(until) {
		return errLinuxPerformanceDirTime
	}
	return nil
}

// Readdirnames(n>0) neither sorts nor stats entries/FD targets. Only one batch
// is retained. At the per-directory entry limit, a one-name probe distinguishes
// exact EOF from overflow; the probe is also charged to the global item budget.
// Deadlines are cooperative: they cannot interrupt an in-flight kernel syscall.
func (c *linuxPerformanceCollector) walkNames(path string, b *linuxPerformanceScanBudget, limit int, until time.Time, visit func(string) error) (int, error) {
	if err := b.check(until); err != nil {
		return 0, err
	}
	var dir linuxPerformanceDirectory
	var err error
	if c.openDir != nil {
		dir, err = c.openDir(path)
	} else {
		dir, err = os.Open(path)
	}
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	count := 0
	for {
		if err := b.check(until); err != nil {
			return 0, err
		}
		n := min(linuxPerformanceDirBatch, max(1, limit-count), b.remaining)
		names, err := dir.Readdirnames(n)
		b.remaining -= len(names)
		if budgetErr := b.check(until); budgetErr != nil {
			return 0, budgetErr
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if count+len(names) > limit {
			return 0, errLinuxPerformanceDirLimit
		}
		count += len(names)
		if visit != nil {
			for _, name := range names {
				if err := b.check(until); err != nil {
					return 0, err
				}
				if err := visit(name); err != nil {
					return 0, err
				}
			}
			if err := b.check(until); err != nil {
				return 0, err
			}
		}
		if errors.Is(err, io.EOF) {
			return count, nil
		}
		if len(names) == 0 {
			return 0, io.ErrNoProgress
		}
	}
}

func newPerformanceCollector(rootPID int, directory string) (performanceCollector, error) {
	c, err := newLinuxPerformanceCollector(rootPID, directory, "/proc")
	if err != nil {
		return nil, err
	}
	return c, nil
}

// procDirectory is injectable for deterministic exit/reuse and degraded-procfs tests.
// Everything retained here is immutable; Collect owns all per-sample state.
func newLinuxPerformanceCollector(rootPID int, directory, procDirectory string) (*linuxPerformanceCollector, error) {
	if rootPID <= 0 {
		return nil, errors.New("performance root PID must be positive")
	}
	c := &linuxPerformanceCollector{
		rootPID: rootPID, directory: directory, procDirectory: procDirectory,
		pageSize: uint64(os.Getpagesize()),
	}
	auxv, err := readLinuxPerformanceFile(filepath.Join(procDirectory, "self", "auxv"), 64<<10)
	if err == nil {
		c.clockTicks, err = parseLinuxPerformanceClockTicks(auxv, strconv.IntSize/8, binary.NativeEndian)
	}
	if err != nil {
		// USER_HZ is not necessarily 100. Neither CLOCKS_PER_SEC nor the kernel's
		// CONFIG_HZ is a substitute for AT_CLKTCK on the current process ABI.
		c.clockUnavailable = "unavailable: AT_CLKTCK could not be read reliably"
	}
	root, err := c.readStat(rootPID)
	if err == nil {
		c.rootStartID = root.startID()
	} else {
		// Never attach to a later occupant of a PID whose initial identity was
		// unknown. System/runtime diagnostics can still continue without it.
		c.rootUnavailable = "root identity unavailable: " + linuxPerformanceReadReason(err)
	}
	c.system = c.readSystem()
	return c, nil
}

func (c *linuxPerformanceCollector) System() PerformanceSystem {
	s := c.system
	s.Unavailable = make(map[string]string, len(c.system.Unavailable))
	for key, reason := range c.system.Unavailable {
		s.Unavailable[key] = reason
	}
	return s
}

func (*linuxPerformanceCollector) Close() {}

func (c *linuxPerformanceCollector) readSystem() PerformanceSystem {
	s := PerformanceSystem{
		OS: runtime.GOOS, ProcessArch: runtime.GOARCH, LogicalCPUs: runtime.NumCPU(),
		Unavailable: map[string]string{"gpus": linuxPerformanceGPU},
	}
	var uts unix.Utsname
	if unix.Uname(&uts) == nil {
		// Do not use nodename, domainname, or version (the build string may
		// contain a builder's account/hostname). Only release and machine.
		s.OSVersion = unix.ByteSliceToString(uts.Release[:])
		s.NativeArch = linuxPerformanceNativeArch(unix.ByteSliceToString(uts.Machine[:]))
	}
	if s.OSVersion == "" {
		s.Unavailable["os_version"] = "unavailable: kernel release could not be read"
	}
	if s.NativeArch == "" {
		s.Unavailable["native_arch"] = "unavailable: native architecture could not be read"
	}
	if data, err := readLinuxPerformanceFile(filepath.Join(c.procDirectory, "stat"), 4<<20); err == nil {
		cpus := make(map[uint64]struct{})
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
				continue
			}
			if id, err := strconv.ParseUint(strings.TrimPrefix(fields[0], "cpu"), 10, 32); err == nil {
				cpus[id] = struct{}{}
			}
		}
		if len(cpus) > 0 {
			s.LogicalCPUs = len(cpus)
		} else {
			s.Unavailable["logical_cpus"] = "system count unavailable; using runtime affinity-visible CPU count"
		}
	} else {
		s.Unavailable["logical_cpus"] = "system count unavailable; using runtime affinity-visible CPU count"
	}
	s.CPUName = readLinuxPerformanceCPUName(filepath.Join(c.procDirectory, "cpuinfo"))
	if s.CPUName == "" {
		s.Unavailable["cpu_name"] = "unavailable: CPU model is not exposed by procfs"
	}
	data, err := readLinuxPerformanceFile(filepath.Join(c.procDirectory, "meminfo"), 1<<20)
	if err == nil {
		if total, ok := parseLinuxPerformanceCounters(data, true, "MemTotal")["MemTotal"]; ok && total > 0 {
			s.TotalRAMBytes = total
		}
	}
	if s.TotalRAMBytes == 0 {
		s.Unavailable["total_ram_bytes"] = "unavailable: physical memory total could not be read"
	}
	return s
}

func (c *linuxPerformanceCollector) Collect() performancePlatformSample {
	s := performancePlatformSample{
		Values: make(map[string]float64),
		Unavailable: map[string]string{
			"gpu": linuxPerformanceGPU, "gpu_engines": linuxPerformanceGPU,
			"system_gpu_busiest_engine_percent": linuxPerformanceGPU,
			"tree_gpu_busiest_engine_percent":   linuxPerformanceGPU,
		},
	}
	b := newLinuxPerformanceScanBudget(c.scanNow)
	var queue []linuxPerformanceStat
	// Always secure the root's bounded basic counters first. FD/task leakage
	// must not delay its CPU/IO timestamp or consume the budget before it runs.
	if c.rootStartID == "" {
		s.Unavailable["processes"] = c.rootUnavailable
	} else if root, err := c.readStat(c.rootPID); err != nil {
		s.Unavailable["processes"] = linuxPerformanceReadReason(err)
	} else if root.startID() != c.rootStartID {
		s.Unavailable["processes"] = "unavailable: root PID was reused; refusing unrelated processes"
	} else if process, err := c.collectProcess(root); err != nil {
		s.Unavailable["processes"] = linuxPerformanceReadReason(err)
	} else {
		queue = append(queue, root)
		s.Processes = append(s.Processes, process)
	}
	if err := b.check(time.Time{}); err == nil {
		c.collectSystem(&s)
	} else {
		for _, key := range linuxPerformanceMemoryKeys {
			s.Unavailable[key] = linuxPerformanceReadReason(err)
		}
		s.Unavailable["system_cpu_total_seconds"] = linuxPerformanceReadReason(err)
		s.Unavailable["system_cpu_idle_seconds"] = linuxPerformanceReadReason(err)
	}
	if err := b.check(time.Time{}); err == nil {
		c.collectDisk(&s)
	} else {
		s.Unavailable["disk_total_bytes"] = linuxPerformanceReadReason(err)
		s.Unavailable["disk_free_bytes"] = linuxPerformanceReadReason(err)
	}
	seen := map[int]bool{c.rootPID: true}
	for i := 0; i < len(queue); i++ {
		if b.check(time.Time{}) != nil {
			break
		}
		stat := queue[i]
		if i > 0 {
			process, err := c.collectProcess(stat)
			if err != nil {
				linuxPerformanceTreeProblem(&s, "incomplete: a process exited or changed identity while sampling")
				continue
			}
			s.Processes = append(s.Processes, process)
		}
		c.collectFDs(&s.Processes[len(s.Processes)-1], b)
		children, problem := c.children(stat, b)
		if problem != "" {
			linuxPerformanceTreeProblem(&s, problem)
		}
		var candidates []linuxPerformanceStat
		for _, pid := range children {
			if b.check(time.Time{}) != nil {
				break
			}
			if seen[pid] {
				continue
			}
			if len(queue)+len(candidates) >= linuxPerformanceMaxProcesses {
				s.Unavailable["process_tree"] = "incomplete: " + errLinuxPerformanceTreeLimit.Error()
				break
			}
			child, err := c.readStat(pid)
			if err != nil || child.parentPID != stat.pid || child.startTime < stat.startTime {
				linuxPerformanceTreeProblem(&s, "incomplete: a child exited, was reparented, or changed identity")
				continue
			}
			candidates = append(candidates, child)
		}
		// Bracket child discovery with identity checks; a new occupant of the
		// parent's PID must not introduce its unrelated children into the tree.
		if len(candidates) > 0 && b.check(time.Time{}) == nil {
			current, err := c.readStat(stat.pid)
			if err != nil || current.startTime != stat.startTime {
				linuxPerformanceTreeProblem(&s, "incomplete: parent identity changed during child discovery")
				continue
			}
			for _, child := range candidates {
				seen[child.pid] = true
				queue = append(queue, child)
			}
		}
	}
	// One final bounded identity read is allowed even after budget exhaustion.
	// It must not overwrite the earlier CounterAt with the slow scan's end time.
	if len(queue) > 0 {
		current, err := c.readStat(c.rootPID)
		if err != nil || current.startID() != c.rootStartID {
			s.Processes = nil
			s.Unavailable["processes"] = "unavailable: root exited or changed identity during sampling"
		}
	}
	if err := b.check(time.Time{}); err != nil {
		s.Unavailable["collection_budget"] = linuxPerformanceReadReason(err)
		linuxPerformanceTreeProblem(&s, "incomplete: collection scan budget exhausted")
	}
	s.AllProcesses = c.allProcesses()
	return s
}

// allProcesses reads one stat line per process for the system-wide table. It
// mirrors the Windows collector so the whole pipeline can be exercised here.
func (c *linuxPerformanceCollector) allProcesses() []performanceSystemProcess {
	entries, err := os.ReadDir(c.procDirectory)
	if err != nil {
		return nil
	}
	var out []performanceSystemProcess
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || len(out) >= 32768 {
			continue
		}
		stat, err := c.readStat(pid)
		if err != nil {
			continue // Exited between ReadDir and the read.
		}
		p := performanceSystemProcess{PID: pid, ParentPID: stat.parentPID, Name: stat.name, StartID: stat.startID()}
		if threads, ok := stat.uintField(20); ok {
			p.Threads = int(threads)
		}
		if pages, ok := stat.uintField(24); ok && c.pageSize > 0 {
			p.WorkingSet = float64(pages) * float64(c.pageSize)
		}
		user, userOK := stat.uintField(14)
		system, systemOK := stat.uintField(15)
		if userOK && systemOK && c.clockTicks > 0 {
			p.CPUSeconds = (float64(user) + float64(system)) / float64(c.clockTicks)
		}
		out = append(out, p)
	}
	return out
}

func linuxPerformanceTreeProblem(s *performancePlatformSample, reason string) {
	if s.Unavailable["process_tree"] == "" {
		s.Unavailable["process_tree"] = reason
	}
}

// Only enumerate tasks and their direct children, never global /proc/*/stat or
// other applications' names. Children can belong to any thread, not just the
// leader. Kernels/procfs mounts without task/children give an explicit partial
// tree rather than a guessed complete tree or a global-process fallback.
func (c *linuxPerformanceCollector) children(parent linuxPerformanceStat, b *linuxPerformanceScanBudget) ([]int, string) {
	until := b.now().Add(linuxPerformanceTaskTime)
	base := filepath.Join(c.procDirectory, strconv.Itoa(parent.pid), "task")
	ids := make(map[int]bool)
	var children []int
	problem := ""
	count, err := c.walkNames(base, b, linuxPerformanceMaxTasks, until, func(task string) error {
		if tid, err := strconv.Atoi(task); err != nil || tid <= 0 {
			return nil
		}
		data, err := readLinuxPerformanceFile(filepath.Join(base, task, "children"), linuxPerformanceChildrenSize)
		if err != nil {
			problem = "incomplete: task children unavailable, oversized, or a thread exited"
			return nil
		}
		// FieldsSeq avoids allocating a slice for an entire leaked child list.
		for field := range strings.FieldsSeq(string(data)) {
			if err := b.check(until); err != nil {
				return err
			}
			b.remaining--
			pid, err := strconv.Atoi(field)
			if err != nil || pid <= 0 {
				problem = "incomplete: invalid task children data"
				continue
			}
			if !ids[pid] {
				ids[pid] = true
				children = append(children, pid)
				if len(children) > linuxPerformanceMaxProcesses {
					return errLinuxPerformanceTreeLimit
				}
			}
		}
		return nil
	})
	if err != nil {
		problem = "incomplete task/child discovery: " + linuxPerformanceDirectoryReason(err, linuxPerformanceMaxTasks, linuxPerformanceTaskTime)
	} else if count == 0 {
		problem = "incomplete: no process tasks could be observed"
	}
	if err := b.check(time.Time{}); err != nil {
		return nil, "incomplete task/child discovery: " + linuxPerformanceReadReason(err)
	}
	current, err := c.readStat(parent.pid)
	if err != nil || current.startTime != parent.startTime {
		return nil, "incomplete: parent exited or changed identity during child discovery"
	}
	return children, problem
}

func (c *linuxPerformanceCollector) collectFDs(p *PerformanceProcess, b *linuxPerformanceScanBudget) {
	until := b.now().Add(linuxPerformanceFDTime)
	count, err := c.walkNames(filepath.Join(c.procDirectory, strconv.Itoa(p.PID), "fd"), b, linuxPerformanceMaxFDs, until, nil)
	if err == nil {
		err = b.check(until) // Include directory Close before starting another read.
	}
	if err == nil {
		var current linuxPerformanceStat
		current, err = c.readStat(p.PID)
		if err == nil && (current.startID() != p.StartID || current.parentPID != p.ParentPID) {
			err = errLinuxPerformanceData
		}
		if err == nil {
			err = b.check(until)
		}
	}
	if err != nil {
		// No partial/lower-bound FD count is ever reported as an exact gauge.
		delete(p.Values, "fds")
		p.Unavailable["fds"] = linuxPerformanceDirectoryReason(err, linuxPerformanceMaxFDs, linuxPerformanceFDTime)
		return
	}
	p.Values["fds"] = float64(count)
	delete(p.Unavailable, "fds")
}

func (c *linuxPerformanceCollector) collectProcess(expected linuxPerformanceStat) (PerformanceProcess, error) {
	// Discovery may have happened before a slow sibling scan. Refresh both
	// counters and their timestamp here, not from the queued discovery stat.
	stat, err := c.readStat(expected.pid)
	if err != nil {
		return PerformanceProcess{}, err
	}
	if stat.startTime != expected.startTime || stat.parentPID != expected.parentPID {
		return PerformanceProcess{}, errLinuxPerformanceData
	}
	p := PerformanceProcess{
		PID: stat.pid, ParentPID: stat.parentPID, Name: stat.name, StartID: stat.startID(),
		CounterAt: stat.counterAt, Values: make(map[string]float64),
		Unavailable: map[string]string{
			"fds":                        "unavailable: FD scan skipped by collection budget",
			"private_bytes":              "unsupported: Windows private commit has no equivalent Linux procfs counter",
			"private_resident_bytes":     "not collected: accurate private RSS requires a potentially expensive smaps walk",
			"handles":                    "unsupported: Linux exposes fds, not Windows handles",
			"gdi_objects":                "unsupported: Windows-only resource",
			"user_objects":               "unsupported: Windows-only resource",
			"gpu_busiest_engine_percent": linuxPerformanceGPU,
			"gpu_dedicated_bytes":        linuxPerformanceGPU,
			"gpu_shared_bytes":           linuxPerformanceGPU,
		},
	}
	base := strconv.Itoa(stat.pid)
	// Read I/O adjacent to the actual CPU stat read, before status, FD or task
	// scans. rchar/wchar include cached I/O and pipes/devices; read_bytes /
	// write_bytes instead measure storage traffic and are not interchangeable.
	c.readCounters(filepath.Join(base, "io"), false,
		map[string]string{"rchar": "io_read_bytes", "wchar": "io_write_bytes"}, p.Values, p.Unavailable)
	if p.Name == "" {
		p.Unavailable["name"] = "unavailable: process basename is empty"
	}
	for key, field := range map[string]int{"virtual_bytes": 23, "threads": 20} {
		if n, ok := stat.uintField(field); ok {
			p.Values[key] = float64(n)
		} else {
			p.Unavailable[key] = linuxPerformanceInvalid
		}
	}
	if pages, ok := stat.uintField(24); ok && c.pageSize > 0 && pages <= ^uint64(0)/c.pageSize {
		p.Values["working_set_bytes"] = float64(pages * c.pageSize)
	} else {
		p.Unavailable["working_set_bytes"] = linuxPerformanceInvalid
	}
	minor, minorOK := stat.uintField(10)
	major, majorOK := stat.uintField(12)
	if minorOK && majorOK {
		p.Values["page_faults"] = float64(minor) + float64(major)
	} else {
		p.Unavailable["page_faults"] = linuxPerformanceInvalid
	}
	user, userOK := stat.uintField(14)
	system, systemOK := stat.uintField(15)
	if c.clockTicks == 0 {
		p.Unavailable["cpu_seconds"] = c.clockUnavailable
	} else if userOK && systemOK {
		// Exclude cutime/cstime: descendants are measured separately.
		p.Values["cpu_seconds"] = (float64(user) + float64(system)) / float64(c.clockTicks)
	} else {
		p.Unavailable["cpu_seconds"] = linuxPerformanceInvalid
	}
	c.readCounters(filepath.Join(base, "status"), true,
		map[string]string{"VmHWM": "peak_working_set_bytes"}, p.Values, p.Unavailable)
	current, err := c.readStat(stat.pid)
	if err != nil {
		return PerformanceProcess{}, err
	}
	if current.startTime != stat.startTime || current.parentPID != stat.parentPID {
		return PerformanceProcess{}, errLinuxPerformanceData
	}
	return p, nil
}

func (c *linuxPerformanceCollector) collectSystem(s *performancePlatformSample) {
	c.readCounters("meminfo", true, linuxPerformanceMemoryKeys, s.Values, s.Unavailable)
	data, err := readLinuxPerformanceFile(filepath.Join(c.procDirectory, "stat"), 4<<20)
	reason := linuxPerformanceInvalid
	if c.clockTicks == 0 {
		reason = c.clockUnavailable
	} else if err != nil {
		reason = linuxPerformanceReadReason(err)
	} else if total, idle, ok := parseLinuxPerformanceCPU(data); ok {
		s.Values["system_cpu_total_seconds"] = total / float64(c.clockTicks)
		s.Values["system_cpu_idle_seconds"] = idle / float64(c.clockTicks)
		return
	}
	s.Unavailable["system_cpu_total_seconds"] = reason
	s.Unavailable["system_cpu_idle_seconds"] = reason
}

func (c *linuxPerformanceCollector) collectDisk(s *performancePlatformSample) {
	var stat unix.Statfs_t
	if err := unix.Statfs(c.directory, &stat); err != nil {
		// A PathError or raw directory must never reach the report.
		s.Unavailable["disk_total_bytes"] = "unavailable: report volume statfs failed"
		s.Unavailable["disk_free_bytes"] = "unavailable: report volume statfs failed"
		return
	}
	blockSize := stat.Frsize
	if blockSize <= 0 {
		blockSize = stat.Bsize
	}
	if blockSize <= 0 || stat.Blocks > ^uint64(0)/uint64(blockSize) || stat.Bavail > stat.Blocks {
		s.Unavailable["disk_total_bytes"] = "unavailable: invalid statfs counters"
		s.Unavailable["disk_free_bytes"] = "unavailable: invalid statfs counters"
		return
	}
	s.Values["disk_total_bytes"] = float64(stat.Blocks * uint64(blockSize))
	// Available to the caller, not Bfree (which includes reserved blocks).
	s.Values["disk_free_bytes"] = float64(stat.Bavail * uint64(blockSize))
}

func (c *linuxPerformanceCollector) readCounters(relative string, kb bool, keys map[string]string, values map[string]float64, unavailable map[string]string) {
	wanted := make([]string, 0, len(keys))
	for key := range keys {
		wanted = append(wanted, key)
	}
	data, err := readLinuxPerformanceFile(filepath.Join(c.procDirectory, relative), 64<<10)
	reason := linuxPerformanceInvalid
	var counters map[string]uint64
	if err == nil {
		counters = parseLinuxPerformanceCounters(data, kb, wanted...)
	} else {
		reason = linuxPerformanceReadReason(err)
	}
	for raw, key := range keys {
		if n, ok := counters[raw]; ok {
			values[key] = float64(n)
		} else {
			unavailable[key] = reason
		}
	}
}

type linuxPerformanceStat struct {
	pid, parentPID int
	name           string
	startTime      uint64
	counterAt      time.Time
	fields         []string // Starts at field 3 (state); comm is parsed separately.
}

func (s linuxPerformanceStat) startID() string { return strconv.FormatUint(s.startTime, 10) }

func (s linuxPerformanceStat) uintField(number int) (uint64, bool) {
	index := number - 3
	if index < 0 || index >= len(s.fields) {
		return 0, false
	}
	value, err := strconv.ParseUint(s.fields[index], 10, 64)
	return value, err == nil
}

func (c *linuxPerformanceCollector) readStat(pid int) (linuxPerformanceStat, error) {
	data, err := readLinuxPerformanceFile(filepath.Join(c.procDirectory, strconv.Itoa(pid), "stat"), 64<<10)
	at := time.Now() // Sampling instant, not the end of subsequent FD/task scans.
	if err != nil {
		return linuxPerformanceStat{}, err
	}
	stat, err := parseLinuxPerformanceStat(data)
	if err != nil || stat.pid != pid {
		return linuxPerformanceStat{}, errLinuxPerformanceData
	}
	stat.counterAt = at
	return stat, nil
}

func parseLinuxPerformanceStat(data []byte) (linuxPerformanceStat, error) {
	text := string(data)
	left, right := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
	if left <= 0 || right <= left {
		return linuxPerformanceStat{}, errLinuxPerformanceData
	}
	pid, err := strconv.Atoi(strings.TrimSpace(text[:left]))
	fields := strings.Fields(text[right+1:])
	if err != nil || pid <= 0 || len(fields) < 20 || len(fields[0]) != 1 {
		return linuxPerformanceStat{}, errLinuxPerformanceData
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil || parent < 0 {
		return linuxPerformanceStat{}, errLinuxPerformanceData
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return linuxPerformanceStat{}, errLinuxPerformanceData
	}
	// comm may contain spaces, newlines and either kind of parenthesis. There
	// is no escaping, so only the final ')' separates it from numeric fields.
	name := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, text[left+1:right])
	name = strings.TrimSpace(name)
	if name != "" {
		name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	}
	return linuxPerformanceStat{pid: pid, parentPID: parent, name: name, startTime: start, fields: fields}, nil
}

func parseLinuxPerformanceClockTicks(data []byte, wordBytes int, order binary.ByteOrder) (uint64, error) {
	if (wordBytes != 4 && wordBytes != 8) || len(data)%(2*wordBytes) != 0 {
		return 0, errLinuxPerformanceData
	}
	word := func(b []byte) uint64 {
		if wordBytes == 4 {
			return uint64(order.Uint32(b))
		}
		return order.Uint64(b)
	}
	var ticks uint64
	for offset := 0; offset < len(data); offset += 2 * wordBytes {
		key := word(data[offset:])
		value := word(data[offset+wordBytes:])
		if key == 0 { // AT_NULL
			if ticks > 0 {
				return ticks, nil
			}
			break
		}
		if key == 17 { // AT_CLKTCK
			if value == 0 || ticks != 0 {
				return 0, errLinuxPerformanceData
			}
			ticks = value
		}
	}
	return 0, errLinuxPerformanceData
}

func parseLinuxPerformanceCPU(data []byte) (total, idle float64, ok bool) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "cpu" {
			continue
		}
		if len(fields) < 9 { // user nice system idle iowait irq softirq steal
			return 0, 0, false
		}
		for i := 1; i <= 8; i++ {
			n, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				return 0, 0, false
			}
			total += float64(n)
			if i == 4 || i == 5 {
				idle += float64(n) // Treat iowait as idle, like common CPU monitors.
			}
		}
		// guest and guest_nice are already included in user and nice.
		return total, idle, true
	}
	return 0, 0, false
}

// Read only requested keys; in particular, status identity/account fields and
// cpuinfo serial numbers are never extracted or retained.
func parseLinuxPerformanceCounters(data []byte, kb bool, keys ...string) map[string]uint64 {
	wanted := make(map[string]bool, len(keys))
	seen := make(map[string]bool, len(keys))
	out := make(map[string]uint64, len(keys))
	for _, key := range keys {
		wanted[key] = true
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if !found || !wanted[key] {
			continue
		}
		if seen[key] {
			delete(out, key) // Ambiguous duplicate: omit, even if one was valid.
			continue
		}
		seen[key] = true
		fields := strings.Fields(value)
		if (!kb && len(fields) != 1) || (kb && (len(fields) != 2 || fields[1] != "kB")) {
			continue
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if kb {
			if n > ^uint64(0)/1024 {
				continue
			}
			n *= 1024
		}
		out[key] = n
	}
	return out
}

func readLinuxPerformanceCPUName(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model name", "cpu model", "cpu", "Processor":
			value = strings.TrimSpace(value)
			if value != "" && len(value) <= 256 {
				return value
			}
		}
	}
	return "" // No inferred model from a board name, serial, or architecture.
}

func linuxPerformanceNativeArch(machine string) string {
	switch machine {
	case "x86_64", "amd64":
		return "amd64"
	case "i386", "i486", "i586", "i686":
		return "386"
	case "aarch64", "arm64":
		return "arm64"
	case "ppc64le", "ppc64el":
		return "ppc64le"
	}
	if strings.HasPrefix(machine, "armv") {
		return "arm"
	}
	return machine
}

func readLinuxPerformanceFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		return nil, errLinuxPerformanceData
	}
	return data, err
}

func linuxPerformanceDirectoryReason(err error, limit int, duration time.Duration) string {
	reason := linuxPerformanceReadReason(err)
	if errors.Is(err, errLinuxPerformanceDirLimit) {
		reason += " (" + strconv.Itoa(limit) + " entries)"
	} else if errors.Is(err, errLinuxPerformanceDirTime) {
		reason += " (" + duration.String() + ")"
	}
	return reason
}

func linuxPerformanceReadReason(err error) string {
	switch {
	case errors.Is(err, os.ErrPermission):
		return "unavailable: permission denied"
	case errors.Is(err, os.ErrNotExist):
		return "unavailable: procfs entry missing or process exited"
	case errors.Is(err, errLinuxPerformanceData):
		return linuxPerformanceInvalid
	case errors.Is(err, errLinuxPerformanceScanBudget):
		return "unavailable: " + errLinuxPerformanceScanBudget.Error() + " (" + linuxPerformanceCollectTime.String() + " / " + strconv.Itoa(linuxPerformanceMaxScanItems) + " scan items)"
	case errors.Is(err, errLinuxPerformanceDirLimit):
		return "unavailable: " + errLinuxPerformanceDirLimit.Error()
	case errors.Is(err, errLinuxPerformanceDirTime):
		return "unavailable: " + errLinuxPerformanceDirTime.Error()
	case errors.Is(err, errLinuxPerformanceTreeLimit):
		return "unavailable: " + errLinuxPerformanceTreeLimit.Error()
	default:
		return "unavailable: local API read failed"
	}
}
