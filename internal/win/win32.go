// Package win 封装 Win32 窗口相关 API。
package win

import (
	"syscall"
	"unsafe"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")

	procEnumWindows            = user32.NewProc("EnumWindows")
	procIsWindowVisible        = user32.NewProc("IsWindowVisible")
	procIsWindow               = user32.NewProc("IsWindow")
	procGetWindowTextW         = user32.NewProc("GetWindowTextW")
	procGetClassNameW          = user32.NewProc("GetClassNameW")
	procGetWindowThreadProcess = user32.NewProc("GetWindowThreadProcessId")
	procGetClientRect          = user32.NewProc("GetClientRect")
	procClientToScreen         = user32.NewProc("ClientToScreen")
	procGetWindowRect          = user32.NewProc("GetWindowRect")
	procGetWindowLongW         = user32.NewProc("GetWindowLongW")
	procShowWindow             = user32.NewProc("ShowWindow")
	procSetForegroundWindow    = user32.NewProc("SetForegroundWindow")

	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
)

// Rect 是 Win32 RECT，坐标均为屏幕坐标。
type Rect struct {
	Left, Top, Right, Bottom int32
}

// Width 返回矩形宽度。
func (r Rect) Width() int32 { return r.Right - r.Left }

// Height 返回矩形高度。
func (r Rect) Height() int32 { return r.Bottom - r.Top }

// enumWindowsProc 是 EnumWindows 的回调类型。
type enumWindowsProc func(hwnd uintptr, lParam uintptr) uintptr

func enumWindows(cb enumWindowsProc) error {
	proc := syscall.NewCallback(cb)
	r1, _, e1 := procEnumWindows.Call(proc, 0)
	if r1 == 0 && e1 != syscall.Errno(0) {
		return e1
	}
	return nil
}

func isWindowVisible(hwnd uintptr) bool {
	r1, _, _ := procIsWindowVisible.Call(hwnd)
	return r1 != 0
}

// IsWindow 判断窗口句柄是否仍有效（快速 API，无跨进程消息）。
func IsWindow(hwnd uintptr) bool {
	r1, _, _ := procIsWindow.Call(hwnd)
	return r1 != 0
}

func getWindowText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	r1, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r1 == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:r1])
}

func getClassName(hwnd uintptr) string {
	buf := make([]uint16, 256)
	r1, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r1 == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:r1])
}

func getWindowThreadProcessID(hwnd uintptr) uint32 {
	var pid uint32
	procGetWindowThreadProcess.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}

// getWindowRect 返回窗口在屏幕上的矩形。
func getWindowRect(hwnd uintptr) (Rect, error) {
	var r Rect
	r1, _, e1 := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if r1 == 0 {
		return r, e1
	}
	return r, nil
}

// getClientRect 返回客户区矩形，坐标相对于窗口原点。
func getClientRect(hwnd uintptr) (Rect, error) {
	var r Rect
	r1, _, e1 := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if r1 == 0 {
		return r, e1
	}
	return r, nil
}

func clientToScreen(hwnd uintptr, p *struct{ X, Y int32 }) {
	procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(p)))
}

// ClientScreenRect 返回客户区在屏幕上的矩形。
func ClientScreenRect(hwnd uintptr) (Rect, error) {
	cr, err := getClientRect(hwnd)
	if err != nil {
		return Rect{}, err
	}
	pt := struct{ X, Y int32 }{0, 0}
	clientToScreen(hwnd, &pt)
	return Rect{
		Left:   pt.X,
		Top:    pt.Y,
		Right:  pt.X + cr.Width(),
		Bottom: pt.Y + cr.Height(),
	}, nil
}

const (
	processQueryLimitedInformation = 0x1000
	gwlStyle                       = -16
	wsMinimize                     = 0x20000000
	swRestore                      = 9
)

var (
	user32GetWindowLong       = procGetWindowLongW
	user32ShowWindow          = procShowWindow
	user32SetForegroundWindow = procSetForegroundWindow
)

func openProcess(access uint32, pid uint32) (uintptr, error) {
	r1, _, e1 := procOpenProcess.Call(uintptr(access), 0, uintptr(pid))
	if r1 == 0 {
		return 0, e1
	}
	return r1, nil
}

func closeHandle(h uintptr) {
	procCloseHandle.Call(h)
}

// processName 返回进程的可执行文件名（如 "MuMuPlayer.exe"），失败返回 ""。
func processName(pid uint32) string {
	if pid == 0 {
		return ""
	}
	h, err := openProcess(processQueryLimitedInformation, pid)
	if err != nil {
		return ""
	}
	defer closeHandle(h)

	buf := make([]uint16, 512)
	var size uint32 = uint32(len(buf))
	r1, _, _ := procQueryFullProcessImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r1 == 0 {
		return ""
	}
	full := syscall.UTF16ToString(buf[:size])
	// 只取文件名部分
	idx := -1
	for i := len(full) - 1; i >= 0; i-- {
		if full[i] == '\\' || full[i] == '/' {
			idx = i + 1
			break
		}
	}
	if idx >= 0 {
		return full[idx:]
	}
	return full
}
