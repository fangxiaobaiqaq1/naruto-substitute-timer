//go:build windows && amd64

package mumu

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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
