//go:build !windows

package ui

// Topmost is a native window attribute; other hosts only run the test driver.
func applyTopmost(string, bool) {}
