// Shared LDPlayer data types; console/ADB access lives in client_windows.go.
package leidian

import (
	"fmt"
	"time"
)

const (
	DefaultADBSerialBasePort = 5555
	DefaultADBSerialHost     = "127.0.0.1"
	DefaultADBName           = "adb.exe"
	DefaultConsoleName       = "ldconsole.exe"
)

type Options struct {
	InstallDir  string `json:"install_dir"`
	ConsolePath string `json:"console_path,omitempty"`
	ADBPath     string `json:"adb_path,omitempty"`
	Index       int    `json:"index"`
	Serial      string `json:"serial,omitempty"`
	Package     string `json:"package,omitempty"`
	Connect     bool   `json:"connect_on_start"`
	// CommandTimeout bounds setup commands (adb connect/get-state, ldconsole
	// list2). Zero means 5 s.
	CommandTimeout time.Duration `json:"command_timeout"`
	// CaptureTimeout bounds per-frame commands (screencap). Zero falls back
	// to CommandTimeout.
	CaptureTimeout time.Duration `json:"capture_timeout,omitempty"`
	// Methods is the ordered per-frame capture chain (MethodWindow,
	// MethodADBRaw, MethodADB). Empty means DefaultMethods.
	Methods []string `json:"methods,omitempty"`
	// ADBServerPort is the adb host server port used by MethodADBRaw.
	// Zero means DefaultADBServerPort.
	ADBServerPort int `json:"adb_server_port,omitempty"`
	// TopHWND/BindHWND seed MethodWindow; normally filled from list2.
	TopHWND  uintptr `json:"top_hwnd,omitempty"`
	BindHWND uintptr `json:"bind_hwnd,omitempty"`
}

// Per-frame capture methods. Names match capture.preferredMethods values.
const (
	MethodWindow = "leidian-window"
	MethodADBRaw = "leidian-adb-raw"
	MethodADB    = "leidian-adb"
)

// DefaultMethods is the capture chain used when Options.Methods is empty:
// render-window PrintWindow, then raw adb protocol, then adb exec-out PNG.
func DefaultMethods() []string { return []string{MethodADBRaw, MethodADB} }

// Instance is identified by installation root plus LDPlayer index. The ADB
// serial is derived evidence and can be overridden for non-default mappings.
type Instance struct {
	Root           string `json:"root"`
	Index          int    `json:"index"`
	Name           string `json:"name"`
	Running        bool   `json:"running"`
	ProcessStarted bool   `json:"process_started"`
	AndroidStarted bool   `json:"android_started"`
	PID            int    `json:"pid,omitempty"`
	// TopHWND/BindHWND come from ldconsole list2 fields 3/4 and seed the
	// leidian-window capture mode. They change whenever the instance restarts.
	TopHWND    uintptr `json:"top_hwnd,omitempty"`
	BindHWND   uintptr `json:"bind_hwnd,omitempty"`
	Serial     string  `json:"serial"`
	Resolution string  `json:"resolution,omitempty"`
}

func (i Instance) Label() string {
	state := "未启动"
	if i.AndroidStarted {
		state = "Android 已启动"
	} else if i.ProcessStarted || i.Running {
		state = "进程已启动"
	}
	return fmt.Sprintf("%s · 实例 %d · %s · %s", i.Name, i.Index, state, i.Serial)
}

// Probe executes the same ADB path as live capture and returns structured
// evidence suitable for a cloud agent or support bundle.
type ProbeResult struct {
	StartedAt      time.Time   `json:"started_at"`
	EndedAt        time.Time   `json:"ended_at"`
	Requested      Options     `json:"requested"`
	Resolved       Options     `json:"resolved"`
	Instance       *Instance   `json:"instance,omitempty"`
	State          string      `json:"state,omitempty"`
	Width          int         `json:"width,omitempty"`
	Height         int         `json:"height,omitempty"`
	ImageAvailable bool        `json:"image_available"`
	PackageFound   bool        `json:"package_found,omitempty"`
	Error          string      `json:"error,omitempty"`
	Steps          []ProbeStep `json:"steps"`
}

type ProbeStep struct {
	Stage      string  `json:"stage"`
	OK         bool    `json:"ok"`
	Detail     string  `json:"detail,omitempty"`
	Error      string  `json:"error,omitempty"`
	DurationMS float64 `json:"duration_ms"`
}
