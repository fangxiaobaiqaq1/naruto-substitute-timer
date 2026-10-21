// timer-app 是替身计时器的正式前端入口。
// 从 config.json 装配引擎与 UI，无文件则写出默认配置。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"narutotimer/internal/buildinfo"
	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/safego"
	"narutotimer/internal/ui"
	"narutotimer/internal/updates"
	"narutotimer/internal/win"
	"narutotimer/internal/win32"
)

// gcPercent trims the heap headroom of the live timer. Its live heap is
// dominated by long-lived buffers (capture ring, templates) while each frame
// allocates little, so GOGC=50 lowers the peak RSS by a quarter at 1080p
// (more at higher resolutions) for well under 1% more CPU
// (docs/性能优化-windows.md). An explicit GOGC environment variable wins.
const gcPercent = 50

func main() {
	// First: no DLL may be found next to the executable (a portable EXE in
	// Downloads) or in the working directory/PATH; see internal/win (UR-10).
	_ = win.RestrictDLLSearchPath()
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(gcPercent)
	}
	win.EnablePerMonitorDPIAwareness()
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
	cfg, notes, err := config.LoadOrCreateWithNotes(config.DefaultPath)
	if err != nil {
		fail(err)
	}
	// Not fatal: a read-only directory must not stop the timer.
	crashLog, _ := installCrashLog(cfg.Debug.Directory)
	logConfigNotes(crashLog, notes, time.Now())
	eng, err := factory.NewLive(factory.FromApp(cfg))
	if err != nil {
		fail(err)
	}
	defer eng.Close()
	provider, closeCapture, controlCapture, captureState := frame.NewSelectableSnapshotterConfigStateful(eng, cfg)
	defer closeCapture()
	executable, _ := os.Executable()
	if err := ui.Run(cfg, provider,
		// Live region calibration (docs/区域校准.md): new boxes apply from the next frame.
		ui.WithCalibrationApply(eng.SetCalibration),
		ui.WithTextRecognitionControl(eng.SetEnabled),
		ui.WithCaptureConfigSelection(controlCapture),
		ui.WithCaptureState(captureState),
		ui.WithSupportContext(executable, cfg.Debug.Directory),
		ui.WithInitialAbout(len(os.Args) == 2 && os.Args[1] == "--about")); err != nil {
		fail(err)
	}
}

// crashLogLimit bounds debug/crash.log across a crash loop: a bigger log is
// kept once as crash.log.old and a new one is started.
const crashLogLimit = 1 << 20

// installCrashLog gives the windowed build (-H windowsgui: stderr goes
// nowhere) a trace. A fatal panic or runtime error on any goroutine writes its
// report to <debug>/crash.log, and panics recovered on the capture/analysis
// path (shown as an error in the overlay) are logged there with their stacks.
func installCrashLog(dir string) (*os.File, error) {
	if dir == "" {
		dir = "debug"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "crash.log")
	if info, err := os.Stat(path); err == nil && info.Size() > crashLogLimit {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		f.Close()
		return nil, err
	}
	safego.SetLog(f)
	return f, nil
}

// logConfigNotes records what loading config.json repaired instead of refusing
// the file (calibration boxes outside the size limits, saved by v0.24beta or
// v0.25, now automatic): on stderr and in the debug log, the windowed build's
// only log (stderr goes nowhere there).
func logConfigNotes(log *os.File, notes []string, now time.Time) {
	for _, note := range notes {
		line := fmt.Sprintf("%s config.json: %s\n", now.Format(time.RFC3339), note)
		fmt.Fprint(os.Stderr, line)
		if log != nil {
			_, _ = log.WriteString(line)
		}
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
