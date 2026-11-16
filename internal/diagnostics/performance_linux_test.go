//go:build linux

package diagnostics

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func performanceLinuxStatText(pid, parent int, name string, start, user, system uint64) string {
	fields := make([]string, 22) // Fields 3 through 24.
	for i := range fields {
		fields[i] = "0"
	}
	fields[0], fields[1] = "S", strconv.Itoa(parent)
	fields[7], fields[9] = "7", "3" // minflt, majflt; NOT children's faults.
	fields[8], fields[10] = "800", "900"
	fields[11], fields[12] = strconv.FormatUint(user, 10), strconv.FormatUint(system, 10)
	fields[13], fields[14] = "1000", "2000" // Children's CPU must not be added.
	fields[17], fields[19] = "2", strconv.FormatUint(start, 10)
	fields[20], fields[21] = "4194304", "128"
	return fmt.Sprintf("%d (%s) %s\n", pid, name, strings.Join(fields, " "))
}

func performanceLinuxAuxv(wordBytes int, order binary.ByteOrder, pairs ...[2]uint64) []byte {
	data := make([]byte, len(pairs)*wordBytes*2)
	for i, pair := range pairs {
		for j, value := range pair {
			offset := (i*2 + j) * wordBytes
			if wordBytes == 4 {
				order.PutUint32(data[offset:], uint32(value))
			} else {
				order.PutUint64(data[offset:], value)
			}
		}
	}
	return data
}

func writePerformanceLinuxFixture(t *testing.T, root, relative string, data []byte) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func writePerformanceLinuxProcess(t *testing.T, proc string, pid, parent int, start uint64, name string, children ...int) {
	t.Helper()
	base := strconv.Itoa(pid)
	writePerformanceLinuxFixture(t, proc, filepath.Join(base, "stat"), []byte(performanceLinuxStatText(pid, parent, name, start, 500, 250)))
	writePerformanceLinuxFixture(t, proc, filepath.Join(base, "status"), []byte("VmHWM: 2048 kB\nUid: 1234 1234 1234 1234\n"))
	writePerformanceLinuxFixture(t, proc, filepath.Join(base, "io"), []byte("rchar: 4096\nwchar: 2048\nread_bytes: 16\nwrite_bytes: 8\n"))
	writePerformanceLinuxFixture(t, proc, filepath.Join(base, "fd", "0"), nil)
	ids := make([]string, len(children))
	for i, pid := range children {
		ids[i] = strconv.Itoa(pid)
	}
	writePerformanceLinuxFixture(t, proc, filepath.Join(base, "task", base, "children"), []byte(strings.Join(ids, " ")))
}

func performanceLinuxFixture(t *testing.T) (proc, directory string) {
	t.Helper()
	proc, directory = t.TempDir(), t.TempDir()
	writePerformanceLinuxFixture(t, proc, "self/auxv", performanceLinuxAuxv(strconv.IntSize/8, binary.NativeEndian, [2]uint64{17, 250}, [2]uint64{0, 0}))
	writePerformanceLinuxFixture(t, proc, "stat", []byte("cpu 250 50 100 400 25 15 10 150 75 5\ncpu0 1 2 3 4 5 6 7 8 9 10\ncpu1 1 2 3 4 5 6 7 8 9 10\n"))
	writePerformanceLinuxFixture(t, proc, "cpuinfo", []byte("processor : 0\nmodel name : Fixture CPU\nSerial : DO-NOT-COLLECT-SERIAL\n"))
	writePerformanceLinuxFixture(t, proc, "meminfo", []byte("MemTotal: 4096 kB\nMemAvailable: 2048 kB\nCommitted_AS: 1024 kB\nCommitLimit: 8192 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"))
	writePerformanceLinuxProcess(t, proc, 11, 1, 100, "root")
	return proc, directory
}

func requirePerformanceLinuxCollector(t *testing.T, proc, directory string) *linuxPerformanceCollector {
	t.Helper()
	c, err := newLinuxPerformanceCollector(11, directory, proc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	// Fixture tests exercise item limits deterministically even on a loaded
	// host; deadline tests advance this clock explicitly. CounterAt stays real.
	now := time.Now()
	c.scanNow = func() time.Time { return now }
	return c
}

func performanceLinuxProcessByPID(s performancePlatformSample, pid int) (PerformanceProcess, bool) {
	for _, p := range s.Processes {
		if p.PID == pid {
			return p, true
		}
	}
	return PerformanceProcess{}, false
}

type performanceLinuxNameDirectory struct {
	names    []string
	pos      int
	requests []int
	closed   bool
	readHook func()
	err      error
}

func (d *performanceLinuxNameDirectory) Readdirnames(n int) ([]string, error) {
	if n <= 0 || n > linuxPerformanceDirBatch {
		panic("unbounded directory read")
	}
	d.requests = append(d.requests, n)
	if d.readHook != nil {
		d.readHook()
	}
	if d.err != nil {
		return nil, d.err
	}
	if d.pos == len(d.names) {
		return nil, io.EOF
	}
	end := min(d.pos+n, len(d.names))
	names := d.names[d.pos:end]
	d.pos = end
	return names, nil
}

func (d *performanceLinuxNameDirectory) Close() error {
	d.closed = true
	return nil
}

func performanceLinuxDirectoryNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = strconv.Itoa(20000 + n - i) // Intentionally not sorted.
	}
	return names
}

func TestPerformanceLinuxDirectoryBatchesAndEarlyStop(t *testing.T) {
	for _, size := range []int{0, 1, 64, 65, 130, 131, 4096} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			dir := &performanceLinuxNameDirectory{names: performanceLinuxDirectoryNames(size)}
			c := &linuxPerformanceCollector{openDir: func(string) (linuxPerformanceDirectory, error) { return dir, nil }}
			now := time.Now()
			budget := newLinuxPerformanceScanBudget(func() time.Time { return now })
			var visited []string
			count, err := c.walkNames("unused", budget, 130, now.Add(time.Second), func(name string) error {
				visited = append(visited, name)
				return nil
			})
			if size <= 130 {
				if err != nil || count != size || !slices.Equal(visited, dir.names) {
					t.Fatalf("incomplete/sorted walk: count=%d, err=%v, visited=%v", count, err, visited)
				}
			} else if !errors.Is(err, errLinuxPerformanceDirLimit) || count != 0 || dir.pos != 131 || len(visited) != 130 {
				t.Fatalf("no exact count may escape an incomplete scan: count=%d pos=%d visits=%d err=%v", count, dir.pos, len(visited), err)
			}
			if !dir.closed || len(dir.requests) > 4 {
				t.Fatalf("reader not closed or failed to stop: %+v", dir)
			}
		})
	}
	// Visitor early-stop (e.g. the 64-process cap) must not drain the directory.
	dir := &performanceLinuxNameDirectory{names: performanceLinuxDirectoryNames(4096)}
	c := &linuxPerformanceCollector{openDir: func(string) (linuxPerformanceDirectory, error) { return dir, nil }}
	now := time.Now()
	budget := newLinuxPerformanceScanBudget(func() time.Time { return now })
	visits := 0
	_, err := c.walkNames("unused", budget, 1024, now.Add(time.Second), func(string) error {
		visits++
		return errLinuxPerformanceTreeLimit
	})
	if !errors.Is(err, errLinuxPerformanceTreeLimit) || visits != 1 || len(dir.requests) != 1 || dir.pos != linuxPerformanceDirBatch || !dir.closed {
		t.Fatalf("callback did not stop after one bounded batch: visits=%d pos=%d err=%v", visits, dir.pos, err)
	}
}

func TestPerformanceLinuxFDEntryBudget(t *testing.T) {
	proc, directory := performanceLinuxFixture(t)
	writePerformanceLinuxProcess(t, proc, 11, 1, 100, "root", 12)
	writePerformanceLinuxProcess(t, proc, 12, 11, 110, "child")
	c := requirePerformanceLinuxCollector(t, proc, directory)
	dir := &performanceLinuxNameDirectory{names: performanceLinuxDirectoryNames(4096)}
	c.openDir = func(path string) (linuxPerformanceDirectory, error) {
		if path == filepath.Join(proc, "11", "fd") {
			return dir, nil
		}
		return os.Open(path)
	}
	s := c.Collect()
	p, ok := performanceLinuxProcessByPID(s, 11)
	if !ok || p.Values["cpu_seconds"] != 3 || p.Values["working_set_bytes"] <= 0 || p.Values["io_read_bytes"] != 4096 {
		t.Fatalf("FD leakage prevented root basic counters: %+v", p)
	}
	if _, present := p.Values["fds"]; present || !strings.Contains(p.Unavailable["fds"], "entry budget") {
		t.Fatalf("partial FD count was reported: %+v", p)
	}
	if dir.pos != linuxPerformanceMaxFDs+1 || !dir.closed || len(dir.requests) != linuxPerformanceMaxFDs/linuxPerformanceDirBatch+1 {
		t.Fatalf("FD scan was not batched/bounded: pos=%d requests=%v", dir.pos, dir.requests)
	}
	if _, ok := performanceLinuxProcessByPID(s, 12); !ok {
		t.Fatal("one leaking FD table exhausted descendant collection")
	}
}

func TestPerformanceLinuxTaskEntryBudget(t *testing.T) {
	proc, directory := performanceLinuxFixture(t)
	writePerformanceLinuxProcess(t, proc, 11, 1, 100, "root", 12)
	writePerformanceLinuxProcess(t, proc, 12, 11, 110, "early-child")
	writePerformanceLinuxProcess(t, proc, 13, 11, 120, "late-child")
	c := requirePerformanceLinuxCollector(t, proc, directory)
	dir := &performanceLinuxNameDirectory{names: performanceLinuxDirectoryNames(4096)}
	dir.names[0] = "11"
	// A child reachable only after the thread budget must not be read/reported.
	writePerformanceLinuxFixture(t, proc, filepath.Join("11", "task", dir.names[linuxPerformanceMaxTasks], "children"), []byte("13"))
	c.openDir = func(path string) (linuxPerformanceDirectory, error) {
		if path == filepath.Join(proc, "11", "task") {
			return dir, nil
		}
		return os.Open(path)
	}
	s := c.Collect()
	if !strings.Contains(s.Unavailable["process_tree"], "entry budget") || dir.pos != linuxPerformanceMaxTasks+1 || !dir.closed {
		t.Fatalf("thread scan was not explicitly bounded/incomplete: pos=%d unavailable=%v", dir.pos, s.Unavailable)
	}
	if _, ok := performanceLinuxProcessByPID(s, 12); !ok {
		t.Fatal("valid early child was lost from the partial tree")
	}
	if _, ok := performanceLinuxProcessByPID(s, 13); ok {
		t.Fatal("scanned a thread past the item limit")
	}
	root, _ := performanceLinuxProcessByPID(s, 11)
	if root.Values["threads"] != 2 {
		t.Fatal("thread gauge must come from stat, never a partial task-directory count")
	}
}

func TestPerformanceLinuxChildListBudgets(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(strconv.FormatBool(oversized), func(t *testing.T) {
			proc, directory := performanceLinuxFixture(t)
			writePerformanceLinuxProcess(t, proc, 12, 11, 110, "child")
			list := strings.Repeat("12 ", 2500)
			if oversized {
				list = strings.Repeat("12 ", linuxPerformanceChildrenSize/3+1)
			}
			// Repeated IDs must still consume work budget, not evade it through
			// the deduplication map. Neither list is parsed into a giant slice.
			writePerformanceLinuxFixture(t, proc, "11/task/11/children", []byte(list))
			writePerformanceLinuxFixture(t, proc, "11/task/111/children", []byte(list))
			s := requirePerformanceLinuxCollector(t, proc, directory).Collect()
			if len(s.Processes) != 1 || s.Unavailable["process_tree"] == "" {
				t.Fatalf("unbounded child list was not stopped: %+v", s)
			}
			if oversized {
				if !strings.Contains(s.Unavailable["process_tree"], "oversized") {
					t.Fatal("oversized children data was not explained")
				}
			} else if s.Unavailable["collection_budget"] == "" {
				t.Fatal("child PID tokens were not charged to the global budget")
			}
		})
	}
}

func TestPerformanceLinuxDirectoryTimeBudgets(t *testing.T) {
	for _, kind := range []string{"fd", "task"} {
		t.Run(kind, func(t *testing.T) {
			proc, directory := performanceLinuxFixture(t)
			c := requirePerformanceLinuxCollector(t, proc, directory)
			now := time.Now()
			c.scanNow = func() time.Time { return now }
			delay := linuxPerformanceFDTime
			if kind == "task" {
				delay = linuxPerformanceTaskTime
			}
			dir := &performanceLinuxNameDirectory{names: []string{"11"}, readHook: func() { now = now.Add(delay + time.Nanosecond) }}
			c.openDir = func(path string) (linuxPerformanceDirectory, error) {
				if path == filepath.Join(proc, "11", kind) {
					return dir, nil
				}
				return os.Open(path)
			}
			s := c.Collect()
			p, ok := performanceLinuxProcessByPID(s, 11)
			if !ok || p.CounterAt.IsZero() || len(dir.requests) != 1 || !dir.closed {
				t.Fatalf("timed-out scan did not stop promptly: %+v, calls=%d", s, len(dir.requests))
			}
			if kind == "fd" {
				if _, present := p.Values["fds"]; present || !strings.Contains(p.Unavailable["fds"], "time budget") {
					t.Fatalf("over-time FD count fabricated: %+v", p)
				}
			} else if !strings.Contains(s.Unavailable["process_tree"], "time budget") {
				t.Fatalf("timed-out thread enumeration claimed a complete tree: %v", s.Unavailable)
			}
		})
	}
}

func TestPerformanceLinuxCollectItemBudget(t *testing.T) {
	proc, directory := performanceLinuxFixture(t)
	writePerformanceLinuxProcess(t, proc, 11, 1, 100, "root", 12, 13, 14, 15, 16)
	for pid := 12; pid <= 16; pid++ {
		writePerformanceLinuxProcess(t, proc, pid, 11, uint64(100+pid), "child")
	}
	c := requirePerformanceLinuxCollector(t, proc, directory)
	var readers []*performanceLinuxNameDirectory
	c.openDir = func(path string) (linuxPerformanceDirectory, error) {
		if filepath.Base(path) == "fd" {
			dir := &performanceLinuxNameDirectory{names: performanceLinuxDirectoryNames(linuxPerformanceMaxFDs)}
			readers = append(readers, dir)
			return dir, nil
		}
		return os.Open(path)
	}
	s := c.Collect()
	root, ok := performanceLinuxProcessByPID(s, 11)
	if !ok || root.Values["cpu_seconds"] != 3 || root.Values["fds"] != linuxPerformanceMaxFDs {
		t.Fatalf("root was not prioritized or exact-limit FD count failed: %+v", root)
	}
	if s.Unavailable["collection_budget"] == "" || s.Unavailable["process_tree"] == "" || len(s.Processes) >= 6 {
		t.Fatalf("whole-sample item budget was not enforced: processes=%d unavailable=%v", len(s.Processes), s.Unavailable)
	}
	read := 0
	for _, dir := range readers {
		read += dir.pos
		if !dir.closed {
			t.Fatal("directory left open after budget exhaustion")
		}
	}
	if read > linuxPerformanceMaxScanItems {
		t.Fatalf("global item budget reset for each process: %d names read", read)
	}
	last := s.Processes[len(s.Processes)-1]
	if _, present := last.Values["fds"]; present || !strings.Contains(last.Unavailable["fds"], "collection scan budget") {
		t.Fatalf("partially counted last FD table was reported: %+v", last)
	}
}

func TestPerformanceLinuxCounterAtPrecedesDelayedScan(t *testing.T) {
	proc, directory := performanceLinuxFixture(t)
	writePerformanceLinuxProcess(t, proc, 11, 1, 100, "root", 12)
	writePerformanceLinuxProcess(t, proc, 12, 11, 110, "child")
	c := requirePerformanceLinuxCollector(t, proc, directory)
	now := time.Now()
	c.scanNow = func() time.Time { return now }
	var delayAt time.Time
	dir := &performanceLinuxNameDirectory{names: []string{"0"}}
	dir.readHook = func() {
		delayAt = time.Now()
		// A real tail delay, plus deterministic exhaustion of the global budget.
		time.Sleep(5 * time.Millisecond)
		now = now.Add(linuxPerformanceCollectTime + time.Nanosecond)
		writePerformanceLinuxFixture(t, proc, "11/stat", []byte(performanceLinuxStatText(11, 1, "root", 100, 9999, 9999)))
		writePerformanceLinuxFixture(t, proc, "11/io", []byte("rchar: 9999\nwchar: 9999\n"))
	}
	opens := 0
	c.openDir = func(path string) (linuxPerformanceDirectory, error) {
		opens++
		if path != filepath.Join(proc, "11", "fd") {
			t.Fatalf("continued scanning after the global deadline: %s", path)
		}
		return dir, nil
	}
	started := time.Now()
	s := c.Collect()
	ended := time.Now()
	p, ok := performanceLinuxProcessByPID(s, 11)
	if !ok || p.CounterAt.Before(started) || p.CounterAt.After(delayAt) || ended.Sub(p.CounterAt) < 5*time.Millisecond {
		t.Fatalf("CounterAt stamped at scan completion instead of CPU read: at=%v start=%v delay=%v end=%v", p.CounterAt, started, delayAt, ended)
	}
	if p.Values["cpu_seconds"] != 3 || p.Values["io_read_bytes"] != 4096 || p.Values["io_write_bytes"] != 2048 || p.Values["working_set_bytes"] <= 0 {
		t.Fatalf("basic CPU/IO/RAM must precede slow enumeration: %+v", p)
	}
	if _, present := p.Values["fds"]; present || p.Unavailable["fds"] == "" || s.Unavailable["collection_budget"] == "" || s.Unavailable["process_tree"] == "" {
		t.Fatalf("overall deadline did not cause explicit omissions: %+v", s)
	}
	if opens != 1 || len(dir.requests) != 1 || !dir.closed || len(s.Processes) != 1 {
		t.Fatalf("scan did not stop after first delayed batch: opens=%d reads=%d processes=%d", opens, len(dir.requests), len(s.Processes))
	}
}

func TestPerformanceLinuxQueuedChildCountersAreRefreshed(t *testing.T) {
	proc, directory := performanceLinuxFixture(t)
	writePerformanceLinuxProcess(t, proc, 11, 1, 100, "root", 12, 13)
	writePerformanceLinuxProcess(t, proc, 12, 11, 110, "first-child")
	writePerformanceLinuxProcess(t, proc, 13, 11, 120, "queued-child")
	c := requirePerformanceLinuxCollector(t, proc, directory)
	var changedAt time.Time
	dir := &performanceLinuxNameDirectory{names: []string{"0"}}
	dir.readHook = func() {
		if len(dir.requests) != 1 {
			return
		}
		time.Sleep(time.Millisecond)
		writePerformanceLinuxFixture(t, proc, "13/stat", []byte(performanceLinuxStatText(13, 11, "queued-child", 120, 1000, 500)))
		writePerformanceLinuxFixture(t, proc, "13/io", []byte("rchar: 8192\nwchar: 4096\n"))
		changedAt = time.Now()
	}
	c.openDir = func(path string) (linuxPerformanceDirectory, error) {
		if path == filepath.Join(proc, "12", "fd") {
			return dir, nil
		}
		return os.Open(path)
	}
	p, ok := performanceLinuxProcessByPID(c.Collect(), 13)
	if !ok || changedAt.IsZero() || p.CounterAt.Before(changedAt) || p.Values["cpu_seconds"] != 6 || p.Values["io_read_bytes"] != 8192 {
		t.Fatalf("queued discovery stat was used with later IO/time: %+v, changed=%v", p, changedAt)
	}
}

func TestPerformanceLinuxDirectoryPermissionFailure(t *testing.T) {
	proc, directory := performanceLinuxFixture(t)
	c := requirePerformanceLinuxCollector(t, proc, directory)
	dir := &performanceLinuxNameDirectory{names: performanceLinuxDirectoryNames(128)}
	private := "/PRIVATE-PATH-DO-NOT-REPORT"
	dir.readHook = func() {
		if len(dir.requests) == 2 {
			dir.err = &os.PathError{Op: "readdir", Path: private, Err: os.ErrPermission}
		}
	}
	c.openDir = func(path string) (linuxPerformanceDirectory, error) {
		if filepath.Base(path) == "fd" {
			return dir, nil
		}
		return nil, &os.PathError{Op: "open", Path: private, Err: os.ErrPermission}
	}
	s := c.Collect()
	p, ok := performanceLinuxProcessByPID(s, 11)
	if !ok || p.Values["cpu_seconds"] != 3 || !strings.Contains(p.Unavailable["fds"], "permission denied") || !strings.Contains(s.Unavailable["process_tree"], "permission denied") {
		t.Fatalf("permission failure hid basic counters or its reason: %+v", s)
	}
	if _, present := p.Values["fds"]; present || !dir.closed || dir.pos != linuxPerformanceDirBatch {
		t.Fatal("partial FD count escaped after a mid-enumeration error")
	}
	encoded, err := json.Marshal(s)
	if err != nil || strings.Contains(string(encoded), private) {
		t.Fatalf("directory error exposed a private path: %v", err)
	}
}

func TestPerformanceLinuxStatNames(t *testing.T) {
	for _, name := range []string{"plain", "worker (stage 2)", "a) b)) (c", "(unbalanced", "line\nname) (x)"} {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			stat, err := parseLinuxPerformanceStat([]byte(performanceLinuxStatText(42, 7, name, 98765, 125, 75)))
			if err != nil {
				t.Fatal(err)
			}
			if stat.pid != 42 || stat.parentPID != 7 || stat.startID() != "98765" || stat.name != strings.ReplaceAll(name, "\n", "") {
				t.Fatalf("incorrect comm/identity parsing: %+v", stat)
			}
			for field, want := range map[int]uint64{10: 7, 12: 3, 14: 125, 15: 75, 20: 2, 23: 4194304, 24: 128} {
				if got, ok := stat.uintField(field); !ok || got != want {
					t.Fatalf("field %d = %d, %v; want %d", field, got, ok, want)
				}
			}
		})
	}
	for _, text := range []string{
		"", "42 missing-name 1 2", "42 (unclosed", "42 (name) S 7 0",
		performanceLinuxStatText(-1, 7, "name", 100, 1, 1),
		performanceLinuxStatText(42, -1, "name", 100, 1, 1),
		strings.Replace(performanceLinuxStatText(42, 7, "name", 98765, 1, 1), "98765", "-1", 1),
	} {
		if _, err := parseLinuxPerformanceStat([]byte(text)); err == nil {
			t.Fatalf("malformed stat accepted: %q", text)
		}
	}
	stat, err := parseLinuxPerformanceStat([]byte(performanceLinuxStatText(42, 7, `/private/directory/worker`, 100, 1, 1)))
	if err != nil || stat.name != "worker" {
		t.Fatalf("name is not basename-only: %+v, %v", stat, err)
	}
}

func TestPerformanceLinuxClockTicks(t *testing.T) {
	for _, wordBytes := range []int{4, 8} {
		for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
			for _, ticks := range []uint64{100, 250, 1000} {
				data := performanceLinuxAuxv(wordBytes, order, [2]uint64{6, 4096}, [2]uint64{17, ticks}, [2]uint64{0, 0})
				got, err := parseLinuxPerformanceClockTicks(data, wordBytes, order)
				if err != nil || got != ticks {
					t.Fatalf("word=%d order=%T ticks=%d: got %d, %v", wordBytes, order, ticks, got, err)
				}
			}
		}
	}
	for _, data := range [][]byte{
		nil, {1, 2, 3},
		performanceLinuxAuxv(8, binary.LittleEndian, [2]uint64{0, 0}),
		performanceLinuxAuxv(8, binary.LittleEndian, [2]uint64{17, 0}, [2]uint64{0, 0}),
		performanceLinuxAuxv(8, binary.LittleEndian, [2]uint64{17, 250}), // No terminator.
		performanceLinuxAuxv(8, binary.LittleEndian, [2]uint64{17, 250}, [2]uint64{17, 250}, [2]uint64{0, 0}),
	} {
		if ticks, err := parseLinuxPerformanceClockTicks(data, 8, binary.LittleEndian); err == nil || ticks != 0 {
			t.Fatalf("unreliable auxv yielded ticks=%d, err=%v", ticks, err)
		}
	}
	if _, err := parseLinuxPerformanceClockTicks(nil, 3, binary.LittleEndian); err == nil {
		t.Fatal("invalid ABI word size accepted")
	}
}

func TestPerformanceLinuxCPUAndMemoryParsing(t *testing.T) {
	total, idle, ok := parseLinuxPerformanceCPU([]byte("cpu 250 50 100 400 25 15 10 150 75 5\n"))
	if !ok || total != 1000 || idle != 425 {
		t.Fatalf("guest double-counting or incorrect idle: %v, %v, %v", total, idle, ok)
	}
	for _, text := range []string{"cpu0 1 2 3 4 5 6 7 8", "cpu 1 2 3", "cpu 1 2 -3 4 5 6 7 8", "cpu 1 2 x 4 5 6 7 8"} {
		if _, _, ok := parseLinuxPerformanceCPU([]byte(text)); ok {
			t.Fatalf("malformed CPU counter accepted: %q", text)
		}
	}
	data := []byte("MemTotal: 4096 kB\nSwapTotal: 0 kB\nBadUnit: 12 MB\nNegative: -1 kB\nOverflow: 18446744073709551615 kB\nDuplicate: 1 kB\nDuplicate: 2 kB\nMissingUnit: 2\n")
	values := parseLinuxPerformanceCounters(data, true, "MemTotal", "SwapTotal", "BadUnit", "Negative", "Overflow", "Duplicate", "MissingUnit", "MemAvailable")
	if len(values) != 2 || values["MemTotal"] != 4096*1024 {
		t.Fatalf("invalid memory values were not omitted: %v", values)
	}
	if value, ok := values["SwapTotal"]; !ok || value != 0 {
		t.Fatal("a genuine zero swap total must remain available")
	}
}

func TestPerformanceLinuxFixtureSample(t *testing.T) {
	proc, directory := performanceLinuxFixture(t)
	writePerformanceLinuxProcess(t, proc, 11, 1, 100, "/private/hidden/worker")
	c := requirePerformanceLinuxCollector(t, proc, directory)
	system := c.System()
	if system.OS != "linux" || system.ProcessArch != runtime.GOARCH || system.CPUName != "Fixture CPU" || system.LogicalCPUs != 2 || system.TotalRAMBytes != 4096*1024 {
		t.Fatalf("incorrect system metadata: %+v", system)
	}
	if system.OSVersion == "" || system.NativeArch == "" || !strings.Contains(system.Unavailable["gpus"], "unsupported") {
		t.Fatalf("missing version/arch or GPU unsupported status: %+v", system)
	}
	delete(system.Unavailable, "gpus")
	if c.System().Unavailable["gpus"] == "" {
		t.Fatal("System returned mutable internal metadata")
	}
	s := c.Collect()
	p, ok := performanceLinuxProcessByPID(s, 11)
	if !ok || len(s.Processes) != 1 || p.StartID != "100" || p.Name != "worker" || p.CounterAt.IsZero() {
		t.Fatalf("incorrect process identity: %+v", s)
	}
	for key, want := range map[string]float64{
		"cpu_seconds": 3, "working_set_bytes": float64(128 * os.Getpagesize()),
		"virtual_bytes": 4194304, "page_faults": 10, "threads": 2, "fds": 1,
		"peak_working_set_bytes": 2048 * 1024, "io_read_bytes": 4096, "io_write_bytes": 2048,
	} {
		if got, ok := p.Values[key]; !ok || got != want {
			t.Errorf("%s = %v, present=%v; want %v", key, got, ok, want)
		}
	}
	for key, want := range map[string]float64{
		"system_cpu_total_seconds": 4, "system_cpu_idle_seconds": 1.7,
		"system_memory_total_bytes": 4096 * 1024, "system_memory_available_bytes": 2048 * 1024,
		"system_commit_used_bytes": 1024 * 1024, "system_commit_limit_bytes": 8192 * 1024,
		"system_swap_total_bytes": 0, "system_swap_free_bytes": 0,
	} {
		if got, ok := s.Values[key]; !ok || got != want {
			t.Errorf("%s = %v, present=%v; want %v", key, got, ok, want)
		}
	}
	for _, key := range []string{"private_bytes", "private_resident_bytes", "gpu_busiest_engine_percent", "gpu_dedicated_bytes", "gpu_shared_bytes"} {
		if _, ok := p.Values[key]; ok || p.Unavailable[key] == "" {
			t.Errorf("%s must be omitted with an unavailable reason", key)
		}
	}
	for _, key := range []string{"system_gpu_busiest_engine_percent", "tree_gpu_busiest_engine_percent"} {
		if _, ok := s.Values[key]; ok || !strings.Contains(s.Unavailable[key], "unsupported") {
			t.Errorf("%s must be unsupported, not zero", key)
		}
	}
	if s.Values["disk_total_bytes"] <= 0 || s.Values["disk_free_bytes"] > s.Values["disk_total_bytes"] {
		t.Fatalf("invalid report-volume capacity: %v", s.Values)
	}
	encoded, err := json.Marshal(struct {
		System PerformanceSystem
		Sample performancePlatformSample
	}{c.System(), s})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{proc, directory, "/private/hidden", "DO-NOT-COLLECT-SERIAL", "Uid"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("report leaked %q", secret)
		}
	}
}

func TestPerformanceLinuxProcessExitAndReuse(t *testing.T) {
	proc, directory := performanceLinuxFixture(t)
	writePerformanceLinuxProcess(t, proc, 11, 1, 100, "root", 12, 99)
	writePerformanceLinuxFixture(t, proc, "11/task/111/children", []byte("14 12"))
	writePerformanceLinuxProcess(t, proc, 12, 11, 110, "child", 13)
	writePerformanceLinuxProcess(t, proc, 13, 12, 120, "grandchild")
	writePerformanceLinuxProcess(t, proc, 14, 11, 130, "other-thread-child")
	writePerformanceLinuxProcess(t, proc, 99, 88, 140, "UNRELATED-DO-NOT-REPORT")
	c := requirePerformanceLinuxCollector(t, proc, directory)
	s := c.Collect()
	if len(s.Processes) != 4 {
		t.Fatalf("did not discover all threads' children and grandchild: %+v", s.Processes)
	}
	for _, pid := range []int{11, 12, 13, 14} {
		if _, ok := performanceLinuxProcessByPID(s, pid); !ok {
			t.Errorf("missing descendant %d", pid)
		}
	}
	if _, ok := performanceLinuxProcessByPID(s, 99); ok {
		t.Fatal("a stale child PID introduced an unrelated process")
	}
	oldChild, _ := performanceLinuxProcessByPID(s, 12)
	oldStat, err := c.readStat(12)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(proc, "12")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.collectProcess(oldStat); err == nil {
		t.Fatal("a process exiting after its initial stat read was retained")
	}
	s = c.Collect()
	if _, ok := performanceLinuxProcessByPID(s, 12); ok || s.Unavailable["process_tree"] == "" {
		t.Fatal("exited child must be omitted, with an incomplete-tree reason")
	}
	// The same numeric PID can be a new, legitimate child, but its counters
	// must carry a new StartID so the recorder will not calculate a false delta.
	writePerformanceLinuxProcess(t, proc, 12, 11, 200, "new-child")
	writePerformanceLinuxFixture(t, proc, "12/stat", []byte(performanceLinuxStatText(12, 11, "new-child", 200, 1, 1)))
	if _, err := c.collectProcess(oldStat); err == nil {
		t.Fatal("mixed counters from two occupants of a PID were accepted")
	}
	newChild, ok := performanceLinuxProcessByPID(c.Collect(), 12)
	if !ok || newChild.StartID == oldChild.StartID || newChild.Values["cpu_seconds"] >= oldChild.Values["cpu_seconds"] {
		t.Fatalf("reused child PID identity was not reset: old=%+v new=%+v", oldChild, newChild)
	}
	writePerformanceLinuxProcess(t, proc, 12, 88, 300, "UNRELATED-REUSED-CHILD")
	if _, ok := performanceLinuxProcessByPID(c.Collect(), 12); ok {
		t.Fatal("reused child PID outside the tree was included")
	}
	if err := os.RemoveAll(filepath.Join(proc, "11")); err != nil {
		t.Fatal(err)
	}
	s = c.Collect()
	if len(s.Processes) != 0 || s.Unavailable["processes"] == "" {
		t.Fatal("exited root must not produce fake process measurements")
	}
	writePerformanceLinuxProcess(t, proc, 11, 1, 500, "UNRELATED-REUSED-ROOT", 12)
	s = c.Collect()
	if len(s.Processes) != 0 || !strings.Contains(s.Unavailable["processes"], "reused") {
		t.Fatal("collector attached to a reused root PID")
	}
}

func TestPerformanceLinuxInitiallyUnknownRoot(t *testing.T) {
	proc, directory := performanceLinuxFixture(t)
	if err := os.Remove(filepath.Join(proc, "11", "stat")); err != nil {
		t.Fatal(err)
	}
	c := requirePerformanceLinuxCollector(t, proc, directory)
	writePerformanceLinuxProcess(t, proc, 11, 1, 500, "unknown-occupant")
	s := c.Collect()
	if len(s.Processes) != 0 || s.Unavailable["processes"] == "" || len(s.Values) == 0 {
		t.Fatalf("unknown root identity must not block system metrics or attach later: %+v", s)
	}
}

func TestPerformanceLinuxBoundedTree(t *testing.T) {
	proc, directory := performanceLinuxFixture(t)
	children := make([]int, 70)
	for i := range children {
		children[i] = 100 + i
		writePerformanceLinuxProcess(t, proc, children[i], 11, uint64(200+i), "child")
	}
	writePerformanceLinuxProcess(t, proc, 11, 1, 100, "root", children...)
	s := requirePerformanceLinuxCollector(t, proc, directory).Collect()
	if len(s.Processes) != 64 || !strings.Contains(s.Unavailable["process_tree"], "64") {
		t.Fatalf("process bound not enforced or not reported: count=%d unavailable=%v", len(s.Processes), s.Unavailable)
	}
	seen := make(map[int]bool)
	for _, p := range s.Processes {
		if seen[p.PID] || (p.PID != 11 && p.ParentPID != 11) {
			t.Fatalf("duplicate or unrelated process: %+v", p)
		}
		seen[p.PID] = true
	}
}

func TestPerformanceLinuxUnavailableDoesNotFabricate(t *testing.T) {
	proc, directory := performanceLinuxFixture(t)
	for _, relative := range []string{"self/auxv", "11/io", "11/status", "11/fd", "11/task"} {
		if err := os.RemoveAll(filepath.Join(proc, relative)); err != nil {
			t.Fatal(err)
		}
	}
	writePerformanceLinuxFixture(t, proc, "meminfo", []byte("MemTotal: 4096 kB\nMemFree: 1024 kB\nBuffers: 100 kB\nCached: 100 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\nCommitLimit: -1 kB\n"))
	text := performanceLinuxStatText(11, 1, "root", 100, 500, 250)
	text = strings.Replace(text, "4194304", "invalid", 1)
	text = strings.TrimSuffix(text, "128\n") + "-1\n"
	writePerformanceLinuxFixture(t, proc, "11/stat", []byte(text))
	c := requirePerformanceLinuxCollector(t, proc, filepath.Join(directory, "PRIVATE-PATH-DO-NOT-REPORT", "missing"))
	s := c.Collect()
	p, ok := performanceLinuxProcessByPID(s, 11)
	if !ok {
		t.Fatal("missing optional counters should not discard a valid process identity")
	}
	if s.Unavailable["process_tree"] == "" {
		t.Fatal("missing task children support must not claim a complete tree")
	}
	for _, key := range []string{"cpu_seconds", "virtual_bytes", "working_set_bytes", "io_read_bytes", "io_write_bytes", "fds", "peak_working_set_bytes", "private_bytes"} {
		if _, present := p.Values[key]; present || p.Unavailable[key] == "" {
			t.Errorf("unreliable %s was fabricated or not explained: %+v", key, p)
		}
	}
	for _, key := range []string{"system_cpu_total_seconds", "system_cpu_idle_seconds", "system_memory_available_bytes", "system_commit_used_bytes", "system_commit_limit_bytes", "disk_total_bytes", "disk_free_bytes"} {
		if _, present := s.Values[key]; present || s.Unavailable[key] == "" {
			t.Errorf("unreliable %s was fabricated or not explained: %+v", key, s)
		}
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), directory) || strings.Contains(string(encoded), proc) || strings.Contains(string(encoded), "PRIVATE-PATH-DO-NOT-REPORT") {
		t.Fatal("an API error exposed a full path")
	}
	if c.clockTicks != 0 || !strings.Contains(p.Unavailable["cpu_seconds"], "AT_CLKTCK") {
		t.Fatal("unknown clock ticks were silently replaced by a 100 Hz guess")
	}
}

func TestPerformanceLinuxRejectsInvalidRoot(t *testing.T) {
	for _, pid := range []int{0, -1} {
		collector, err := newPerformanceCollector(pid, t.TempDir())
		if collector != nil || err == nil {
			t.Fatalf("invalid root PID %d: collector=%v, err=%v", pid, collector, err)
		}
	}
}

func TestPerformanceLinuxCurrentProcessResources(t *testing.T) {
	directory := t.TempDir()
	collector, err := newPerformanceCollector(os.Getpid(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Close()
	system := collector.System()
	if system.OS != runtime.GOOS || system.ProcessArch != runtime.GOARCH || system.LogicalCPUs <= 0 {
		t.Fatalf("incorrect runtime metadata: %+v", system)
	}
	previous := collector.Collect()
	previousProcess, ok := performanceLinuxProcessByPID(previous, os.Getpid())
	if !ok {
		t.Fatalf("could not measure current process: %v", previous.Unavailable)
	}
	previousTime := previousProcess.CounterAt
	file, err := os.CreateTemp(directory, "resources-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	workload := make([]byte, 2<<20)
	for i := 0; i < len(workload); i += os.Getpagesize() {
		workload[i] = byte(i + 1) // Commit pages; do not rely on lazy allocation.
	}
	for i := 0; i < 3; i++ {
		if _, err := file.Write(workload); err != nil {
			t.Fatal(err)
		}
		current := collector.Collect()
		process, ok := performanceLinuxProcessByPID(current, os.Getpid())
		if !ok || process.StartID == "" || process.StartID != previousProcess.StartID {
			t.Fatal("current process identity disappeared or changed")
		}
		now := process.CounterAt
		if now.IsZero() || !now.After(previousTime) {
			t.Fatal("missing/non-increasing actual counter timestamp")
		}
		for _, values := range []map[string]float64{current.Values, process.Values} {
			for key, value := range values {
				if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
					t.Errorf("invalid resource measurement %s=%v", key, value)
				}
			}
		}
		for _, key := range []string{"cpu_seconds", "page_faults", "io_read_bytes", "io_write_bytes"} {
			value, have := process.Values[key]
			old, had := previousProcess.Values[key]
			if have && had {
				rate := (value - old) / now.Sub(previousTime).Seconds()
				if rate < 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
					t.Errorf("negative/invalid %s trend: %v -> %v", key, old, value)
				}
			} else if !have && process.Unavailable[key] == "" {
				t.Errorf("absent %s has no unavailable reason", key)
			}
		}
		for _, key := range []string{"working_set_bytes", "virtual_bytes", "threads"} {
			if value, have := process.Values[key]; !have || value <= 0 {
				t.Errorf("missing current-process %s: values=%v unavailable=%v", key, process.Values, process.Unavailable)
			}
		}
		if value, have := process.Values["fds"]; (have && value <= 0) || (!have && process.Unavailable["fds"] == "") {
			t.Errorf("FD count must be positive or explicitly unavailable: %+v", process)
		}
		if value, have := current.Values["system_cpu_total_seconds"]; have && value < previous.Values["system_cpu_total_seconds"] {
			t.Error("system CPU cumulative time decreased")
		}
		previous, previousProcess, previousTime = current, process, now
	}
	runtime.KeepAlive(workload)
}
