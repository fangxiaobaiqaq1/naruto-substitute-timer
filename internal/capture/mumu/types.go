// Shared MuMu data types. They carry no Windows dependency so settings,
// diagnostics and support code can be compiled and unit-tested on any host;
// the SDK, process and manager access lives in the *_windows.go files.
package mumu

import (
	"errors"
	"fmt"
	"time"
)

const (
	// ProbeWorkerArgument is handled by timer-app before it starts OCR or Fyne.
	ProbeWorkerArgument = "--timer-mumu-sdk-probe-v1"

	ProbeStageResolveRoot = "定位 MuMu 安装目录"
	ProbeStageFindSDK     = "定位 MuMu 截图 SDK"
	ProbeStageLoadSDK     = "加载 MuMu 截图 SDK"
	ProbeStageExports     = "检查 MuMu SDK 导出函数"
	ProbeStageConnect     = "连接 MuMu 实例"
	ProbeStageDisplay     = "查询游戏显示层"
	ProbeStageDimensions  = "读取截图尺寸"
	ProbeStagePixels      = "读取截图像素"
	ProbeStageCapture     = "读取一帧游戏画面"
)

// StageError preserves the exact API boundary that failed. Vendor connect
// returns only zero on failure, so no unverified system cause is inferred.
type StageError struct {
	Stage string
	Err   error
}

func (e *StageError) Error() string {
	if e == nil || e.Err == nil {
		return e.Stage
	}
	return e.Stage + "：" + e.Err.Error()
}

func (e *StageError) Unwrap() error { return e.Err }

func stageError(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &StageError{Stage: stage, Err: err}
}

func errorStage(err error) string {
	var target *StageError
	if errors.As(err, &target) {
		return target.Stage
	}
	return ""
}

// SDKFile identifies one SDK DLL without loading it. An installation can
// contain several copies; diagnostic output keeps all of them in a stable order.
type SDKFile struct {
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	Modified  time.Time `json:"modified"`
	SHA256    string    `json:"sha256,omitempty"`
	Preferred bool      `json:"preferred"`
	Error     string    `json:"error,omitempty"`
}

// ProbeStep records an observable SDK boundary. MuMu's connect API only
// returns a handle or zero, so a zero result is intentionally not attributed to
// a guessed cause such as permissions or an incorrect directory.
type ProbeStep struct {
	Stage      string    `json:"stage"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS float64   `json:"duration_ms"`
	OK         bool      `json:"ok"`
	Detail     string    `json:"detail,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// ProbeResult is persisted in a support bundle and can be read without the
// original MuMu installation.
type ProbeResult struct {
	StartedAt      time.Time   `json:"started_at"`
	EndedAt        time.Time   `json:"ended_at"`
	Requested      Options     `json:"requested"`
	ResolvedRoot   string      `json:"resolved_root,omitempty"`
	SDKCandidates  []SDKFile   `json:"sdk_candidates,omitempty"`
	SelectedSDK    SDKFile     `json:"selected_sdk"`
	Width          int         `json:"width,omitempty"`
	Height         int         `json:"height,omitempty"`
	ImageAvailable bool        `json:"image_available"`
	FailureStage   string      `json:"failure_stage,omitempty"`
	Error          string      `json:"error,omitempty"`
	Steps          []ProbeStep `json:"steps"`
}

// ProcessEvidence is operating-system metadata for one MuMu process. Index is
// nil when the process command line does not expose a verified instance number;
// nil must never be rendered as instance 0.
type ProcessEvidence struct {
	PID         uint32 `json:"pid"`
	Name        string `json:"name"`
	Executable  string `json:"executable,omitempty"`
	CommandLine string `json:"command_line,omitempty"`
	Root        string `json:"root,omitempty"`
	Index       *int   `json:"instance_index,omitempty"`
	IndexSource string `json:"index_source,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Installation is a distinct MuMu product tree. Root plus an instance index is
// the connection identity: two installations may each have an instance 0.
type Installation struct {
	Root          string     `json:"root"`
	Sources       []string   `json:"sources,omitempty"`
	ManagerPath   string     `json:"manager_path,omitempty"`
	SDKCandidates []SDKFile  `json:"sdk_candidates,omitempty"`
	Instances     []Instance `json:"instances,omitempty"`
	Error         string     `json:"error,omitempty"`
}

// Inventory keeps every discovered installation, including ones that fail SDK
// or manager inspection, so support can see the complete machine state.
type Inventory struct {
	GeneratedAt   time.Time         `json:"generated_at"`
	Installations []Installation    `json:"installations"`
	Processes     []ProcessEvidence `json:"processes,omitempty"`
	Errors        []string          `json:"errors,omitempty"`
}

func (i Inventory) Summary() string {
	instances, running := 0, 0
	for _, installation := range i.Installations {
		instances += len(installation.Instances)
		for _, instance := range installation.Instances {
			if instance.Running {
				running++
			}
		}
	}
	return fmt.Sprintf("发现 %d 个 MuMu 安装、%d 个实例（运行中 %d 个）", len(i.Installations), instances, running)
}

// Instance is identified by Root plus Index. Index 0 in two different
// installations represents two separate MuMu targets.
type Instance struct {
	Root           string `json:"root"`
	Index          int    `json:"index"`
	Name           string `json:"name"`
	Running        bool   `json:"running"`
	ProcessStarted bool   `json:"process_started"`
	AndroidStarted bool   `json:"android_started"`
	PID            int    `json:"manager_pid,omitempty"`
}

func (i Instance) Label() string {
	state := "未启动"
	if i.AndroidStarted {
		state = "Android 已启动"
	} else if i.ProcessStarted {
		state = "进程已启动"
	}
	return fmt.Sprintf("%s · 实例 %d · %s · %s", i.Name, i.Index, state, i.Root)
}

type Options struct {
	InstallDir string `json:"install_dir"`
	DLLPath    string `json:"dll_path,omitempty"`
	Instance   int    `json:"instance"`
	DisplayID  int    `json:"display_id"`
	Package    string `json:"package,omitempty"`
}

// ProcessChoice is a selectable target. Its identity is Instance.Root plus
// Instance.Index; PID is explanatory evidence and is never used as a saved
// target because it changes when MuMu restarts.
type ProcessChoice struct {
	Instance
	WindowTitle string `json:"window_title,omitempty"`
	ProcessName string `json:"process_name,omitempty"`
	IndexSource string `json:"index_source,omitempty"`
}
