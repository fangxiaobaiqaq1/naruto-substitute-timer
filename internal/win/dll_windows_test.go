//go:build windows

package win

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}

// UR-10: a DLL planted next to the executable must not be found by name once
// the search path is restricted, while System32 DLLs still load by name.
func TestRestrictDLLSearchPathIgnoresExecutableDirectory(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	const planted = "naruto-timer-planted-test.dll"
	path := filepath.Join(filepath.Dir(exe), planted)
	copyFile(t, filepath.Join(system, "version.dll"), path)
	t.Cleanup(func() { os.Remove(path) })

	h, err := windows.LoadLibrary(planted)
	if err != nil {
		t.Fatalf("default search order did not find the planted DLL in the executable directory: %v", err)
	}
	windows.FreeLibrary(h)

	if err := RestrictDLLSearchPath(); err != nil {
		t.Fatal(err)
	}
	if h, err := windows.LoadLibrary(planted); err == nil {
		windows.FreeLibrary(h)
		t.Fatal("planted DLL in the executable directory was loaded after RestrictDLLSearchPath")
	}
	h, err = windows.LoadLibrary("msimg32.dll") // not a KnownDLL
	if err != nil {
		t.Fatalf("System32 DLL no longer loads by name: %v", err)
	}
	windows.FreeLibrary(h)
}

func TestLoadDLLWithDependenciesRequiresAbsolutePath(t *testing.T) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	dll, err := LoadDLLWithDependencies(filepath.Join(system, "msimg32.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dll.FindProc("AlphaBlend"); err != nil {
		t.Fatal(err)
	}
	dll.Release()
	if _, err := LoadDLLWithDependencies("msimg32.dll"); err == nil {
		t.Fatal("relative DLL path accepted")
	}
}
