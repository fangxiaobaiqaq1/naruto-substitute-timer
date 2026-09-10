//go:build windows && amd64 && cgo

package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestLocalWorkerIgnoresInheritedDLLDirectoryAndRepairsCache(t *testing.T) {
	cache, shadow := t.TempDir(), t.TempDir()
	t.Setenv("LOCALAPPDATA", cache)
	// Invalid same-name DLLs reproduce a conflicting inherited DLL search without
	// requiring Java, MuMu or any particular VC version on the test machine.
	for _, name := range runtimeLibraryNames {
		if err := os.WriteFile(filepath.Join(shadow, name), []byte("not a Windows DLL"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// This process-wide setting is only changed during the child launch. No
	// tests in this package run in parallel.
	var b [32768]uint16
	n, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetDllDirectoryW").Call(uintptr(len(b)), uintptr(unsafe.Pointer(&b[0])))
	if n >= uintptr(len(b)) {
		t.Fatalf("DLL directory too long: %v", callErr)
	}
	previous := windows.UTF16ToString(b[:n])
	t.Cleanup(func() { _ = windows.SetDllDirectory(previous) })
	if err := windows.SetDllDirectory(shadow); err != nil {
		t.Fatal(err)
	}
	run := func() response {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], helperArgument)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("worker: %v; %s", err, out)
		}
		var got response
		if err := json.Unmarshal(bytes.TrimSpace(out), &got); err != nil {
			t.Fatal(err)
		}
		if !got.Ready || len(got.RuntimeLibraries) != 5 {
			t.Fatalf("missing native dependency proof: %+v", got)
		}
		for name, path := range got.RuntimeLibraries {
			if !strings.HasPrefix(strings.ToLower(path), strings.ToLower(cache+string(os.PathSeparator))) {
				t.Fatalf("%s fell back outside bundled cache: %s", name, path)
			}
		}
		return got
	}
	got := run()
	license := filepath.Join(filepath.Dir(got.RuntimeLibraries["onnxruntime.dll"]), "licenses", "vcruntime", "VC-RUNTIME-LICENSE.md")
	if data, err := os.ReadFile(license); err != nil || !bytes.Contains(data, []byte("MICROSOFT SOFTWARE LICENSE TERMS")) {
		t.Fatalf("standalone EXE lost upstream license: %v", err)
	}
	path := got.RuntimeLibraries["msvcp140.dll"]
	if err := os.WriteFile(path, []byte("truncated cached runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	run()
	expected, err := vcRuntimeFiles.ReadFile("neuraldata/vcruntime/msvcp140.dll")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, expected) {
		t.Fatal("corrupted DLL not restored from EXE")
	}
}

func TestRuntimeCacheConcurrentRepair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.dll")
	if err := os.WriteFile(path, []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("verified embedded content"), 4096)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ensureRuntimeFile(path, data); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("concurrent repair did not preserve verified content")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v %v", entries, err)
	}
}

func TestRuntimeFailureKeepsCodeAndFullBoundedLog(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	cause := &runtimeLoadError{Library: "onnxruntime.dll", Err: fmt.Errorf("%s: %w", strings.Repeat("long context ", 80), syscall.Errno(1114))}
	summary := runtimeErrorSummary(cause)
	if !strings.Contains(summary, "1114") || strings.Contains(summary, "需要 Microsoft") || len([]rune(summary)) > 180 {
		t.Fatalf("misleading summary: %s", summary)
	}
	for i := 0; i < 4; i++ {
		writeRuntimeFailure(cause)
		writeRuntimeStatus(map[string]string{"onnxruntime.dll": "cache/onnxruntime.dll"})
	}
	data, err := os.ReadFile(filepath.Join(RuntimeLogDirectory(), "ocr-runtime-error.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Error        string
		WindowsError uint32 `json:"windows_error"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Error != cause.Error() || report.WindowsError != 1114 {
		t.Fatalf("original cause was lost: %+v", report)
	}
	entries, err := os.ReadDir(RuntimeLogDirectory())
	if err != nil || len(entries) != 2 {
		t.Fatalf("unbounded log growth: %v %v", entries, err)
	}
}
