//go:build windows && amd64

package support

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"narutotimer/internal/capture/mumu"
)

// ProbeSDK runs the vendor SDK in a short-lived copy of the app. A hung native
// call therefore cannot freeze the Fyne process; CommandContext kills only the
// helper it started on timeout.
func ProbeSDK(ctx context.Context, executable string, options mumu.Options) (mumu.ProbeResult, error) {
	if executable == "" {
		return mumu.ProbeResult{}, fmt.Errorf("无法定位计时器可执行文件")
	}
	dir, err := os.MkdirTemp("", "naruto-timer-mumu-probe-")
	if err != nil {
		return mumu.ProbeResult{}, err
	}
	defer os.RemoveAll(dir)
	request := filepath.Join(dir, "request.json")
	resultPath := filepath.Join(dir, "result.json")
	data, err := json.Marshal(options)
	if err != nil {
		return mumu.ProbeResult{}, err
	}
	if err := os.WriteFile(request, data, 0o600); err != nil {
		return mumu.ProbeResult{}, err
	}
	command := exec.CommandContext(ctx, executable, mumu.ProbeWorkerArgument, request, resultPath)
	output, runErr := command.CombinedOutput()
	resultData, readErr := os.ReadFile(resultPath)
	if readErr == nil {
		var result mumu.ProbeResult
		if err := json.Unmarshal(resultData, &result); err != nil {
			return mumu.ProbeResult{}, err
		}
		// A completed probe writes a structured result even when the SDK reports
		// a failed connect. That is a diagnostic outcome, not a worker crash.
		return result, nil
	}
	if runErr != nil {
		return mumu.ProbeResult{}, fmt.Errorf("SDK 自检未产生结果: %w；%s", runErr, string(output))
	}
	return mumu.ProbeResult{}, readErr
}
