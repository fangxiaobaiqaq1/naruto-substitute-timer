//go:build !windows

package win

import (
	"errors"
	"image"
)

var errUnsupported = errors.New("Win32 窗口访问仅支持 Windows")

// Window mirrors the Windows definition so shared code compiles on any host.
type Window struct {
	HWND        uintptr
	PID         uint32
	ProcessName string
	Title       string
	ClassName   string
	Visible     bool
}

type Rect struct {
	Left, Top, Right, Bottom int32
}

func (r Rect) Width() int32  { return r.Right - r.Left }
func (r Rect) Height() int32 { return r.Bottom - r.Top }

func ListWindows() []Window                      { return nil }
func FindMuMu() []Window                         { return nil }
func InvalidateMuMu()                            {}
func RestoreWindow(uintptr) error                { return errUnsupported }
func IsIconic(uintptr) bool                      { return false }
func IsWindow(uintptr) bool                      { return false }
func CaptureClient(uintptr) (*image.RGBA, error) { return nil, errUnsupported }
func IsBusyFrame(error) bool                     { return false }
func ClientScreenRect(uintptr) (Rect, error)     { return Rect{}, errUnsupported }
func EnablePerMonitorDPIAwareness()              {}

func FindLeidianRender(uintptr, uintptr) (uintptr, error) { return 0, errUnsupported }
