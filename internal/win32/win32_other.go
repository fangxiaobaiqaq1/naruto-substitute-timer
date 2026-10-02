//go:build !windows

package win32

import "errors"

var errUnsupported = errors.New("Win32 API 仅支持 Windows")

func HasConsole() bool                                           { return true }
func MsgBoxError(title, text string)                             {}
func SetWindowOpacity(uintptr, float64) error                    { return errUnsupported }
func ApplyWindowAlpha(string, byte)                              {}
func SetWindowFrameless(uintptr, bool, uintptr) (uintptr, error) { return 0, errUnsupported }
func SetWindowTopmost(uintptr, bool) error                       { return errUnsupported }
func RefitFrameless(uintptr, uintptr) error                      { return errUnsupported }
func CursorPos() (int32, int32, error)                           { return 0, 0, errUnsupported }
func WindowPos(uintptr) (int32, int32, error)                    { return 0, 0, errUnsupported }
func MoveWindowTo(uintptr, int32, int32) error                   { return errUnsupported }
