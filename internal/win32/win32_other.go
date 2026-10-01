//go:build !windows

package win32

import "errors"

var errUnsupported = errors.New("Win32 API 仅支持 Windows")

func HasConsole() bool                        { return true }
func MsgBoxError(title, text string)          {}
func SetWindowOpacity(uintptr, float64) error { return errUnsupported }
func ApplyWindowAlpha(string, byte)           {}
