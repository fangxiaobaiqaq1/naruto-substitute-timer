//go:build !windows

package updates

import "errors"

// ApplyArgument matches the Windows updater so command parsing stays identical.
const ApplyArgument = "--timer-apply-update"

var errUnsupported = errors.New("在线更新的自替换仅支持 Windows")

func PrepareRestart(string, Release) error { return errUnsupported }
func ApplyFile(string) error               { return errUnsupported }
