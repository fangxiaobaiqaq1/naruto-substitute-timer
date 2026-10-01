//go:build windows

package ui

import (
	"syscall"
	"unsafe"

	"narutotimer/internal/win32"
)

func applyTopmost(title string, on bool) {
	ptr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	find := user32.NewProc("FindWindowW")
	hwnd, _, _ := find.Call(0, uintptr(unsafe.Pointer(ptr)))
	if hwnd == 0 {
		return
	}
	h := win32.HWNDNoTopMost
	if on {
		h = win32.HWNDTopMost
	}
	win32.ProcSetWindowPos.Call(hwnd, h, 0, 0, 0, 0,
		uintptr(win32.SWPNoMove|win32.SWPNoSize|win32.SWPNoActivate))
}
