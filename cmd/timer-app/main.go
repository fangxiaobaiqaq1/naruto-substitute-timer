// timer-app 是替身计时器的正式前端入口。
// 从 config.json 装配引擎与 UI，无文件则写出默认配置。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"narutotimer/internal/buildinfo"
	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/ui"
	"narutotimer/internal/updates"
	"narutotimer/internal/win32"
)

func main() {
	if len(os.Args) == 4 && os.Args[1] == mumu.ProbeWorkerArgument {
		runMuMuProbeWorker(os.Args[2], os.Args[3])
		return
	}
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
	provider, closeCapture, controlCapture, captureState := frame.NewSelectableSnapshotterStateful(eng, cfg)
	defer closeCapture()
	executable, _ := os.Executable()
	if err := ui.Run(cfg, provider,
		ui.WithTextRecognitionControl(eng.SetEnabled),
		ui.WithCaptureSelection(controlCapture),
		ui.WithCaptureState(captureState),
		ui.WithSupportContext(executable, cfg.Debug.Directory),
		ui.WithInitialAbout(len(os.Args) == 2 && os.Args[1] == "--about")); err != nil {
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

func runMuMuProbeWorker(requestPath, resultPath string) {
	data, err := os.ReadFile(requestPath)
	if err != nil {
		fail(err)
	}
	var options mumu.Options
	if err := json.Unmarshal(data, &options); err != nil {
		fail(fmt.Errorf("读取 MuMu 自检请求: %w", err))
	}
	result := mumu.Probe(options)
	data, err = json.MarshalIndent(result, "", "  ")
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(resultPath, append(data, '\n'), 0o600); err != nil {
		fail(err)
	}
	if result.Error != "" {
		os.Exit(2)
	}
}
