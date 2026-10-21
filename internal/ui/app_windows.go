//go:build windows

package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

func newFyneApp() fyne.App { return app.NewWithID("narutotimer.app") }
