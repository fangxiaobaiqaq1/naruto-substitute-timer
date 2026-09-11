//go:build windows && amd64

package mumu

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// FindDLLCandidates returns SDK files in deterministic preference order. The
// old implementation selected the last glob result, which could depend on the
// file-system enumeration order when multiple nx_device versions existed.
func FindDLLCandidates(root string) ([]SDKFile, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("MuMu 安装目录为空")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(absolute)
	type candidate struct {
		path      string
		preferred bool
	}
	candidates := []candidate{
		{filepath.Join(root, "nx_main", "sdk", "external_renderer_ipc.dll"), true},
		{filepath.Join(root, "shell", "sdk", "external_renderer_ipc.dll"), true},
	}
	device, _ := filepath.Glob(filepath.Join(root, "nx_device", "*", "shell", "sdk", "external_renderer_ipc.dll"))
	sort.Strings(device)
	for _, path := range device {
		candidates = append(candidates, candidate{path: path})
	}
	seen := map[string]bool{}
	out := make([]SDKFile, 0, len(candidates))
	for _, candidate := range candidates {
		key := strings.ToLower(filepath.Clean(candidate.path))
		if seen[key] {
			continue
		}
		seen[key] = true
		info, err := os.Stat(candidate.path)
		if err != nil || info.IsDir() {
			continue
		}
		out = append(out, SDKFile{
			Path:      candidate.path,
			Size:      info.Size(),
			Modified:  info.ModTime(),
			Preferred: candidate.preferred,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("MuMu screenshot SDK not found under %q", root)
	}
	return out, nil
}

func inspectSDK(file SDKFile) SDKFile {
	f, err := os.Open(file.Path)
	if err != nil {
		file.Error = err.Error()
		return file
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		file.Error = err.Error()
		return file
	}
	file.SHA256 = fmt.Sprintf("%x", hash.Sum(nil))
	return file
}

func addProbeStep(result *ProbeResult, stage string, started time.Time, detail string, err error) {
	step := ProbeStep{
		Stage:      stage,
		StartedAt:  started,
		DurationMS: float64(time.Since(started).Microseconds()) / 1000,
		OK:         err == nil,
		Detail:     detail,
	}
	if err != nil {
		step.Error = err.Error()
		result.Error = err.Error()
		result.FailureStage = errorStage(err)
		if result.FailureStage == "" {
			result.FailureStage = stage
		}
	}
	result.Steps = append(result.Steps, step)
}

// Probe uses the exact Open/Capture implementation used by live collection. It
// should be called from a background goroutine because a vendor SDK call can
// block independently of the Fyne event loop.
func Probe(options Options) (result ProbeResult) {
	result = ProbeResult{StartedAt: time.Now(), Requested: options}
	defer func() { result.EndedAt = time.Now() }()

	started := time.Now()
	root := strings.TrimSpace(options.InstallDir)
	if root == "" {
		found, err := DiscoverInstallation()
		if err != nil {
			addProbeStep(&result, ProbeStageResolveRoot, started, "", err)
			return result
		}
		root = found
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		addProbeStep(&result, ProbeStageResolveRoot, started, "", err)
		return result
	}
	result.ResolvedRoot = filepath.Clean(absolute)
	options.InstallDir = result.ResolvedRoot
	addProbeStep(&result, ProbeStageResolveRoot, started, result.ResolvedRoot, nil)

	started = time.Now()
	candidates, err := FindDLLCandidates(result.ResolvedRoot)
	if err != nil {
		addProbeStep(&result, ProbeStageFindSDK, started, "", err)
		return result
	}
	for index := range candidates {
		candidates[index] = inspectSDK(candidates[index])
	}
	result.SDKCandidates = candidates
	if options.DLLPath == "" {
		options.DLLPath = candidates[0].Path
	}
	for _, candidate := range candidates {
		if strings.EqualFold(filepath.Clean(candidate.Path), filepath.Clean(options.DLLPath)) {
			result.SelectedSDK = candidate
			break
		}
	}
	if result.SelectedSDK.Path == "" {
		info, statErr := os.Stat(options.DLLPath)
		if statErr != nil {
			addProbeStep(&result, ProbeStageFindSDK, started, "", statErr)
			return result
		}
		result.SelectedSDK = inspectSDK(SDKFile{Path: options.DLLPath, Size: info.Size(), Modified: info.ModTime()})
	}
	addProbeStep(&result, ProbeStageFindSDK, started, result.SelectedSDK.Path, nil)

	started = time.Now()
	client, err := Open(options)
	if err != nil {
		stage := errorStage(err)
		if stage == "" {
			stage = ProbeStageConnect
		}
		addProbeStep(&result, stage, started, "", err)
		return result
	}
	if client.DLLPath() != "" {
		for _, candidate := range result.SDKCandidates {
			if strings.EqualFold(filepath.Clean(candidate.Path), filepath.Clean(client.DLLPath())) {
				result.SelectedSDK = candidate
				break
			}
		}
	}
	addProbeStep(&result, ProbeStageConnect, started, client.Source(), nil)
	defer client.Close()

	started = time.Now()
	img, err := client.Capture()
	if err != nil {
		stage := errorStage(err)
		if stage == "" {
			stage = ProbeStageCapture
		}
		addProbeStep(&result, stage, started, "", err)
		return result
	}
	result.Width, result.Height = img.Bounds().Dx(), img.Bounds().Dy()
	result.ImageAvailable = true
	addProbeStep(&result, ProbeStageCapture, started, fmt.Sprintf("%d × %d", result.Width, result.Height), nil)
	return result
}
