//go:build windows && amd64

package support

import (
	"context"
	"encoding/json"
	"fmt"
	"narutotimer/internal/capture/helper"
	"narutotimer/internal/capture/mumu"
)

// ProbeSDK runs the vendor SDK in a short-lived copy of the app. A hung native
// call therefore cannot freeze the Fyne process; CommandContext kills only the
// helper it started on timeout.
func ProbeSDK(ctx context.Context, executable string, options mumu.Options) (mumu.ProbeResult, error) {
	if executable == "" {
		return mumu.ProbeResult{}, fmt.Errorf("无法定位计时器可执行文件")
	}
	data, err := json.Marshal(options)
	if err != nil {
		return mumu.ProbeResult{}, err
	}
	resultData, runErr := helper.Run(ctx, executable, mumu.ProbeWorkerArgument, data)
	if runErr != nil {
		return mumu.ProbeResult{}, fmt.Errorf("SDK 自检未产生结果: %w", runErr)
	}
	var result mumu.ProbeResult
	if err := json.Unmarshal(resultData, &result); err != nil {
		return mumu.ProbeResult{}, err
	}
	// A completed probe writes a structured result even when the SDK reports
	// a failed connect. That is a diagnostic outcome, not a worker crash.
	return result, nil
}
