// timer-app 是替身计时器的正式前端入口。
// 从 config.json 装配引擎与 UI，无文件则写出默认配置。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"narutotimer/internal/buildinfo"
	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/ui"
	"narutotimer/internal/updates"
	"narutotimer/internal/win32"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(buildinfo.Version)
		return
	}
	if len(os.Args) == 3 && os.Args[1] == updates.ApplyArgument {
		if err := updates.ApplyFile(os.Args[2]); err != nil {
			fail(err)
		}
		return
	}

	go updates.PruneCache(time.Now())
	// Resolve config beside the executable, independent of shortcut working directory.
	if executable, err := os.Executable(); err == nil {
		dir, err := writableApplicationDirectory(executable)
		if err != nil {
			fail(err)
		}
		if err := os.Chdir(dir); err != nil {
			fail(err)
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
	provider, closeCapture, controlCapture := frame.NewSelectableSnapshotter(eng, cfg)
	defer closeCapture()
	if err := ui.Run(cfg, provider, ui.WithTextRecognitionControl(eng.SetEnabled), ui.WithCaptureSelection(controlCapture), ui.WithInitialAbout(len(os.Args) == 2 && os.Args[1] == "--about")); err != nil {
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

func applicationDirectory(executable string) string {
	dir := filepath.Dir(executable)
	if strings.EqualFold(filepath.Base(dir), "bin") {
		root := filepath.Dir(dir)
		for _, marker := range []string{"config.json", "config.example.json", "assets/templates/manifest.json"} {
			if info, err := os.Stat(filepath.Join(root, marker)); err == nil && !info.IsDir() {
				return root
			}
		}
	}
	return dir
}

func writableApplicationDirectory(executable string) (string, error) {
	dir := applicationDirectory(executable)
	if _, err := os.Stat(filepath.Join(filepath.Dir(executable), "installed.marker")); err == nil {
		root, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(root, "NarutoTimer")
		if err = os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
	}
	return dir, nil
}
