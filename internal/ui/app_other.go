//go:build !windows

package ui

import "fyne.io/fyne/v2"

// The desktop driver needs cgo/X11 on other hosts; those hosts only compile and
// unit-test the overlay with the Fyne test driver, so no application is created.
func newFyneApp() fyne.App { return nil }
