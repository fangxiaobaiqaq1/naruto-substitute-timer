// timer-app 是替身计时器的正式前端入口。
// 从 config.json 装配引擎与 UI，无文件则写出默认配置。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/ui"
	"narutotimer/internal/win32"
)

func main() {
	// A direct double-click of bin/timer-app.exe must use the same root
	// configuration and relative assets as the root launcher.
	if executable, err := os.Executable(); err == nil {
		binDir := filepath.Dir(executable)
		if strings.EqualFold(filepath.Base(binDir), "bin") {
			root := filepath.Dir(binDir)
			if _, err := os.Stat(filepath.Join(root, "assets", "templates", "manifest.json")); err == nil {
				if err := os.Chdir(root); err != nil {
					fail(err)
				}
			}
		}
	}
	cfg, err := config.LoadOrCreate(config.DefaultPath)
	if err != nil {
		fail(err)
	}
	eng, err := factory.NewLive(factory.FromApp(cfg))
	if err != nil {
		fail(err)
	}
	defer eng.Close()
	provider, closeCapture := frame.NewConfiguredSnapshotter(eng, cfg)
	defer closeCapture()
	if err := ui.Run(cfg, provider, ui.WithTextRecognitionControl(eng.SetEnabled)); err != nil {
		fail(err)
	}
}

func fail(err error) {
	if win32.HasConsole() {
		fmt.Fprintln(os.Stderr, "timer-app:", err)
	} else {
		win32.MsgBoxError("替身计时器", err.Error())
	}
	os.Exit(1)
}
