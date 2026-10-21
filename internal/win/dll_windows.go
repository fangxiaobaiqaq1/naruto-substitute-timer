//go:build windows

package win

import (
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// RestrictDLLSearchPath makes System32 the only implicit DLL search location
// for the rest of the process (UR-10). Without it, every LoadLibrary by name
// (GLFW's dinput8, xinput1_*, dwmapi; lazy DLLs such as msimg32) searches the
// executable's own folder first, so a timer-app.exe run from Downloads would
// load a DLL planted next to it. Call it first thing in main.
//
// Loads that pass explicit search flags are unaffected: the OCR worker loads
// its runtime by full path with LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|SYSTEM32,
// and LoadLibraryEx(LOAD_WITH_ALTERED_SEARCH_PATH) callers (COM in-process
// servers, input methods) still get their own directory. Imports in the EXE
// header (OPENGL32.dll) are resolved by the loader before main runs and are
// not covered.
func RestrictDLLSearchPath() error {
	return windows.SetDefaultDllDirectories(windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
}

// LoadDLLWithDependencies loads a DLL by absolute path and resolves the DLL's
// own imports from its directory and System32 only (never the timer's folder,
// the working directory or PATH). Use it for third-party DLLs such as the MuMu
// screenshot SDK.
func LoadDLLWithDependencies(path string) (*syscall.DLL, error) {
	if !filepath.IsAbs(path) {
		return nil, &syscall.DLLError{Err: windows.ERROR_INVALID_PARAMETER, ObjName: path, Msg: "DLL path must be absolute: " + path}
	}
	h, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, &syscall.DLLError{Err: err, ObjName: path, Msg: "Failed to load " + path + ": " + err.Error()}
	}
	return &syscall.DLL{Name: path, Handle: syscall.Handle(h)}, nil
}
