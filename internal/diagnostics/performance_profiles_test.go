package diagnostics

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func performanceProfileTestField(data []byte, field uint64, payload []byte) []byte {
	data = binary.AppendUvarint(data, field<<3|2)
	data = binary.AppendUvarint(data, uint64(len(payload)))
	return append(data, payload...)
}

func performanceProfileTestGzip(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func performanceProfileTestGunzip(t *testing.T, compressed []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, performanceProfileMaxDecoded+1))
	if err != nil || int64(len(data)) > performanceProfileMaxDecoded {
		t.Fatalf("invalid sanitized gzip: bytes=%d err=%v", len(data), err)
	}
	return data
}

// Independent small decoder used to inspect the resulting table, not to rewrite
// it. Unknown fields are skipped according to their wire type.
func performanceProfileTestStrings(t *testing.T, raw []byte) []string {
	t.Helper()
	r := bytes.NewReader(raw)
	var table []string
	for r.Len() > 0 {
		tag, err := binary.ReadUvarint(r)
		if err != nil {
			t.Fatal(err)
		}
		switch tag & 7 {
		case 0:
			_, err = binary.ReadUvarint(r)
		case 1:
			_, err = io.CopyN(io.Discard, r, 8)
		case 5:
			_, err = io.CopyN(io.Discard, r, 4)
		case 2:
			var n uint64
			n, err = binary.ReadUvarint(r)
			if err != nil || n > uint64(r.Len()) {
				t.Fatalf("bad length %d: %v", n, err)
			}
			data := make([]byte, int(n))
			_, err = io.ReadFull(r, data)
			if tag>>3 == 6 {
				table = append(table, string(data))
			}
		default:
			t.Fatalf("unexpected wire type %d", tag&7)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return table
}

func TestPerformanceProfilePathBasenames(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"/home/PRIVATE/src/main.go", "main.go"},
		{`C:\Users\PRIVATE\src with spaces\main.go`, "main.go"},
		{"d:/Users/PRIVATE/main.go", "main.go"},
		{`\\server\PRIVATE\share\main.go`, "main.go"},
		{"//server/PRIVATE/main.go", "main.go"},
		{`\\?\C:\PRIVATE\main.go`, "main.go"},
		{`\\?\UNC\server\PRIVATE\main.go`, "main.go"},
		{"/home/PRIVATE/directory/", "directory"},
		{"/", "[path]"},
		{`C:\`, "[path]"},
		{"", ""},
		{"narutotimer/internal/main.go", "narutotimer/internal/main.go"},
		{`C:relative\main.go`, `C:relative\main.go`},
		{"runtime/pprof.writeHeap", "runtime/pprof.writeHeap"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := performanceProfilePathBase(tc.path); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPerformanceProfileStringIndexesAndWirePreservation(t *testing.T) {
	// Sample-type and mapping messages contain string-table indexes. Their
	// payloads, unknown fields, fixed-width values and noncanonical varints must
	// survive byte-for-byte; equal basenames must NOT merge table entries.
	prefix := performanceProfileTestField(nil, 1, []byte{8, 1, 16, 2})
	prefix = performanceProfileTestField(prefix, 3, []byte{8, 1, 40, 3})
	prefix = append(prefix, 0x98, 0x01, 0x80, 0x00)             // Unknown field 19, varint zero.
	prefix = append(prefix, 0xa1, 0x01, 1, 2, 3, 4, 5, 6, 7, 8) // Field 20, fixed64.
	prefix = append(prefix, 0xad, 0x01, 1, 2, 3, 4)             // Field 21, fixed32.
	prefix = append(prefix, 0xb2, 0x01, 0x81, 0x00, 'x')        // Field 22, length one.
	prefix = binary.AppendUvarint(prefix, ((1<<29)-1)<<3)       // Largest legal tag and uint64 varint.
	prefix = binary.AppendUvarint(prefix, ^uint64(0))
	table := []string{"", "alloc_objects", "count", "/home/PRIVATE/bin/app", `C:\Users\PRIVATE\main.go`, `\\server\PRIVATE\other.go`, "relative/main.go", "/a/shared.go", "/b/shared.go"}
	want := []string{"", "alloc_objects", "count", "app", "main.go", "other.go", "relative/main.go", "shared.go", "shared.go"}
	raw, expected := bytes.Clone(prefix), bytes.Clone(prefix)
	for i := range table {
		raw = performanceProfileTestField(raw, 6, []byte(table[i]))
		expected = performanceProfileTestField(expected, 6, []byte(want[i]))
	}
	// An unchanged string-table entry also retains its original tag/length.
	raw = append(raw, 0xb2, 0x00, 0x81, 0x00, 'z')
	expected = append(expected, 0xb2, 0x00, 0x81, 0x00, 'z')
	want = append(want, "z")
	var out bytes.Buffer
	if err := sanitizePerformanceHeapProfile(bytes.NewReader(performanceProfileTestGzip(t, raw)), &out, performanceProfileMaxDecoded, performanceProfileMaxField); err != nil {
		t.Fatal(err)
	}
	decoded := performanceProfileTestGunzip(t, out.Bytes())
	if !bytes.Equal(decoded, expected) {
		t.Fatal("changed fields other than absolute string-table paths")
	}
	got := performanceProfileTestStrings(t, decoded)
	if len(got) != len(want) {
		t.Fatalf("table size changed: %d != %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d = %q, want %q", i, got[i], want[i])
		}
	}
	if bytes.Contains(decoded, []byte("PRIVATE")) || bytes.Contains(decoded, []byte("server")) {
		t.Fatal("private path retained")
	}
}

func TestPerformanceProfileRejectsMalformedWireAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"zero tag", []byte{0}, errPerformanceProfileFormat},
		{"large tag", binary.AppendUvarint(nil, 1<<32), errPerformanceProfileFormat},
		{"start group", []byte{0x0b}, errPerformanceProfileFormat},
		{"end group", []byte{0x0c}, errPerformanceProfileFormat},
		{"wire six", []byte{0x0e}, errPerformanceProfileFormat},
		{"wire seven", []byte{0x0f}, errPerformanceProfileFormat},
		{"table wrong wire", []byte{0x30, 0}, errPerformanceProfileFormat},
		{"tag overflow", bytes.Repeat([]byte{0xff}, 10), errPerformanceProfileFormat},
		{"varint overflow", append([]byte{8}, append(bytes.Repeat([]byte{0x80}, 9), 2)...), errPerformanceProfileFormat},
		{"truncated tag", []byte{0x80}, io.ErrUnexpectedEOF},
		{"missing varint", []byte{8}, io.ErrUnexpectedEOF},
		{"truncated varint", []byte{8, 0x80}, io.ErrUnexpectedEOF},
		{"missing length", []byte{0x0a}, io.ErrUnexpectedEOF},
		{"truncated length", []byte{0x0a, 0x80}, io.ErrUnexpectedEOF},
		{"truncated payload", []byte{0x0a, 2, 1}, io.ErrUnexpectedEOF},
		{"truncated string", []byte{0x32, 2, 'x'}, io.ErrUnexpectedEOF},
		{"truncated fixed64", []byte{0x09, 1}, io.ErrUnexpectedEOF},
		{"truncated fixed32", []byte{0x0d, 1}, io.ErrUnexpectedEOF},
		{"oversized field", binary.AppendUvarint([]byte{0x0a}, uint64(performanceProfileMaxField+1)), errPerformanceProfileLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := append([]byte{0x32, 0}, tc.data...)
			err := sanitizePerformanceProfileProto(bufio.NewReader(bytes.NewReader(raw)), io.Discard, performanceProfileMaxField)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	for _, raw := range [][]byte{nil, {8, 0}, {0x32, 1, 'x'}} {
		if err := sanitizePerformanceProfileProto(bufio.NewReader(bytes.NewReader(raw)), io.Discard, performanceProfileMaxField); err == nil {
			t.Fatal("accepted missing/invalid string_table[0]")
		}
	}
	raw := performanceProfileTestField([]byte{0x32, 0}, 20, bytes.Repeat([]byte{'x'}, 128))
	compressed := performanceProfileTestGzip(t, raw)
	if err := sanitizePerformanceHeapProfile(bytes.NewReader(compressed), io.Discard, int64(len(raw)), 128); err != nil {
		t.Fatalf("exact decoded limit: %v", err)
	}
	if err := sanitizePerformanceHeapProfile(bytes.NewReader(compressed), io.Discard, int64(len(raw)-1), 128); !errors.Is(err, errPerformanceProfileLimit) {
		t.Fatalf("decoded limit not enforced: %v", err)
	}
	badCRC := bytes.Clone(compressed)
	badCRC[len(badCRC)-8] ^= 1
	for name, data := range map[string][]byte{
		"header": []byte("not gzip"), "trailer": compressed[:len(compressed)-1],
		"checksum": badCRC, "trailing garbage": append(bytes.Clone(compressed), 'x'),
	} {
		t.Run(name, func(t *testing.T) {
			if err := sanitizePerformanceHeapProfile(bytes.NewReader(data), io.Discard, performanceProfileMaxDecoded, performanceProfileMaxField); err == nil {
				t.Fatal("invalid gzip was reported successful")
			}
		})
	}
}

func TestPerformanceProfileGoroutineLines(t *testing.T) {
	input := "goroutine 12 [running]:\nexample.org/pkg.Func({0x1, 0x2})\n" +
		"\t/home/PRIVATE/src/main.go:42 +0x99 fp=0x1 sp=0x2 pc=0x3\n" +
		"created by pkg.Main in goroutine 1\n" +
		"\tC:\\Users\\PRIVATE\\src with spaces\\worker.go:7 +0x4\r\n" +
		"\t\\\\server\\PRIVATE\\share\\other.go:8 +0xa\n" +
		"\tnarutotimer/internal/main.go:99 +0xb\n" +
		"\t/private/file:123 name.go:9 +0xc"
	want := "goroutine 12 [running]:\nexample.org/pkg.Func({0x1, 0x2})\n" +
		"\tmain.go:42 +0x99 fp=0x1 sp=0x2 pc=0x3\n" +
		"created by pkg.Main in goroutine 1\n" +
		"\tworker.go:7 +0x4\r\n\tother.go:8 +0xa\n" +
		"\tnarutotimer/internal/main.go:99 +0xb\n\tfile:123 name.go:9 +0xc"
	var out bytes.Buffer
	if err := sanitizePerformanceGoroutineProfile(strings.NewReader(input), &out, performanceProfileMaxDecoded, performanceProfileMaxLine); err != nil {
		t.Fatal(err)
	}
	if out.String() != want {
		t.Fatalf("stack changed unexpectedly:\n%s", out.String())
	}
	for _, tc := range []struct {
		name, text string
		bytes      int64
		line       int
		want       error
	}{
		{"line limit", strings.Repeat("x", 33), 64, 32, errPerformanceProfileLimit},
		{"total limit", strings.Repeat("x\n", 33), 64, 32, errPerformanceProfileLimit},
		{"partial path", "\t/home/PRIVATE/truncated", 64, 32, errPerformanceProfileFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := sanitizePerformanceGoroutineProfile(strings.NewReader(tc.text), io.Discard, tc.bytes, tc.line); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	if err := sanitizePerformanceGoroutineProfile(strings.NewReader("abc\n"), io.Discard, 4, 4); err != nil {
		t.Fatalf("exact text limits: %v", err)
	}
}

type performanceProfileTestWriter func([]byte) (int, error)

func (w performanceProfileTestWriter) Write(p []byte) (int, error) { return w(p) }

func TestPerformanceProfileStreamCancelsAndJoins(t *testing.T) {
	// Incompressible data keeps the producer blocked on the pipe when the
	// destination fails. Completion channels check joining, not goroutine-count
	// heuristics or sleeps. These are small bounded fixtures, not a benchmark.
	payload := make([]byte, 128<<10)
	_, _ = rand.New(rand.NewSource(1)).Read(payload)
	heap := performanceProfileTestGzip(t, performanceProfileTestField([]byte{0x32, 0}, 20, payload))
	text := []byte(strings.Repeat("goroutine 1 [running]:\n\t/home/PRIVATE/main.go:42 +0x1\n", 4096))
	heapSanitizer := func(r io.Reader, w io.Writer) error {
		return sanitizePerformanceHeapProfile(r, w, performanceProfileMaxDecoded, performanceProfileMaxField)
	}
	textSanitizer := func(r io.Reader, w io.Writer) error {
		return sanitizePerformanceGoroutineProfile(r, w, performanceProfileMaxDecoded, performanceProfileMaxLine)
	}
	writeErr := errors.New("injected destination failure")
	for mode, fixture := range map[string]struct {
		raw      []byte
		sanitize func(io.Reader, io.Writer) error
	}{"heap": {heap, heapSanitizer}, "text": {text, textSanitizer}} {
		for name, failure := range map[string]struct {
			writer performanceProfileTestWriter
			want   error
		}{
			"error": {func([]byte) (int, error) { return 0, writeErr }, writeErr},
			"short": {func([]byte) (int, error) { return 0, nil }, io.ErrShortWrite},
			"panic": {func([]byte) (int, error) { panic("/home/PRIVATE/panic") }, errPerformanceProfilePanic},
		} {
			t.Run(mode+"/"+name, func(t *testing.T) {
				joined := make(chan struct{})
				result := make(chan error, 1)
				go func() {
					result <- streamSanitizedPerformanceProfile(failure.writer, func(w io.Writer) error {
						defer close(joined)
						_, err := io.Copy(w, bytes.NewReader(fixture.raw))
						return err
					}, fixture.sanitize)
				}()
				select {
				case err := <-result:
					if !errors.Is(err, failure.want) || strings.Contains(err.Error(), "PRIVATE") {
						t.Fatalf("got %v, want safe %v", err, failure.want)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("producer was not cancelled")
				}
				select {
				case <-joined:
				default:
					t.Fatal("helper returned before producer exited")
				}
			})
		}
	}
	for name, failure := range map[string]struct {
		raw      []byte
		sanitize func(io.Reader, io.Writer) error
		want     error
	}{
		"bad wire": {performanceProfileTestGzip(t, append([]byte{0x32, 0, 0}, payload...)), heapSanitizer, errPerformanceProfileFormat},
		"decoded limit": {heap, func(r io.Reader, w io.Writer) error {
			return sanitizePerformanceHeapProfile(r, w, 64, performanceProfileMaxField)
		}, errPerformanceProfileLimit},
		"line limit": {text, func(r io.Reader, w io.Writer) error {
			return sanitizePerformanceGoroutineProfile(r, w, performanceProfileMaxDecoded, 8)
		}, errPerformanceProfileLimit},
	} {
		t.Run(name, func(t *testing.T) {
			joined := make(chan struct{})
			result := make(chan error, 1)
			go func() {
				result <- streamSanitizedPerformanceProfile(io.Discard, func(w io.Writer) error {
					defer close(joined)
					_, err := io.Copy(w, bytes.NewReader(failure.raw))
					return err
				}, failure.sanitize)
			}()
			select {
			case err := <-result:
				if !errors.Is(err, failure.want) {
					t.Fatalf("got %v, want %v", err, failure.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("parse failure did not cancel producer")
			}
			select {
			case <-joined:
			default:
				t.Fatal("parse failure did not join producer")
			}
		})
	}
	valid := performanceProfileTestGzip(t, []byte{0x32, 0})
	for _, panicAfterWrite := range []bool{false, true} {
		err := streamSanitizedPerformanceProfile(io.Discard, func(w io.Writer) error {
			if panicAfterWrite {
				_, _ = w.Write(valid)
			}
			panic("/home/PRIVATE/producer")
		}, heapSanitizer)
		if !errors.Is(err, errPerformanceProfilePanic) || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatalf("producer panic escaped or was lost: %v", err)
		}
	}
	producerErr := errors.New("injected producer failure after valid gzip")
	if err := streamSanitizedPerformanceProfile(io.Discard, func(w io.Writer) error {
		_, _ = w.Write(valid)
		return producerErr
	}, heapSanitizer); !errors.Is(err, producerErr) {
		t.Fatalf("late producer failure lost: %v", err)
	}
	writes := 0
	lateFailure := performanceProfileTestWriter(func(p []byte) (int, error) {
		writes++
		if writes == 1 { // Accept the gzip header; fail while Close flushes the tiny body.
			return len(p), nil
		}
		return 0, writeErr
	})
	if err := heapSanitizer(bytes.NewReader(valid), lateFailure); !errors.Is(err, writeErr) {
		t.Fatalf("gzip finalization error lost: %v", err)
	}
}

func TestPerformanceProfileActualHeapAndGoroutines(t *testing.T) {
	var heap, stacks bytes.Buffer
	if err := writeSanitizedPerformanceProfile("heap", &heap, 0); err != nil {
		t.Fatal(err)
	}
	if err := writeSanitizedPerformanceProfile("goroutine", &stacks, 2); err != nil {
		t.Fatal(err)
	}
	raw := performanceProfileTestGunzip(t, heap.Bytes())
	table := performanceProfileTestStrings(t, raw)
	if len(table) == 0 || table[0] != "" {
		t.Fatal("invalid actual string table")
	}
	for _, text := range table {
		if performanceProfileAbsolutePath(text) {
			t.Fatal("absolute path survived in actual heap string table")
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mappingFound := false
	for _, text := range table {
		mappingFound = mappingFound || text == filepath.Base(executable)
	}
	if (runtime.GOOS == "linux" || runtime.GOOS == "windows") && !mappingFound {
		t.Fatal("actual executable mapping basename was not retained")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{executable, runtime.GOROOT(), cwd} {
		if bytes.Contains(raw, []byte(private)) || strings.Contains(stacks.String(), private) {
			t.Fatal("actual executable or source path survived")
		}
	}
	if !strings.Contains(stacks.String(), "goroutine ") || !strings.Contains(stacks.String(), "performance_profiles.go:") {
		t.Fatal("stack/debug information was lost")
	}
	// Only the sanitized output is written to disk. A real pprof parser also
	// validates nested messages and all the string indexes we kept opaque.
	path := filepath.Join(t.TempDir(), "heap-sanitized.pprof")
	if err := os.WriteFile(path, heap.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goTool += ".exe"
	}
	if _, err := os.Stat(goTool); err != nil {
		t.Skip("Go tool unavailable for additional pprof interoperability check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, goTool, "tool", "pprof", "-raw", path).CombinedOutput()
	if err != nil {
		t.Fatalf("go tool pprof rejected sanitized heap: %v\n%s", err, output)
	}
	if !bytes.Contains(output, []byte("Samples:")) {
		t.Fatal("go tool pprof did not decode samples")
	}
}

func TestPerformanceProfileRejectsUnsupportedModes(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		debug int
	}{{"missing", 0}, {"heap", 2}, {"goroutine", 0}, {"allocs", 0}} {
		var out bytes.Buffer
		if err := writeSanitizedPerformanceProfile(tc.kind, &out, tc.debug); err == nil || out.Len() != 0 {
			t.Fatalf("unsupported mode wrote output: %+v, err=%v", tc, err)
		}
	}
}
