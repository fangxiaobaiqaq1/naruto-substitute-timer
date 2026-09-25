//go:build windows && amd64

package mumu

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"time"
)

// RunCaptureWorker serves the runtime helper protocol before normal app startup.
func RunCaptureWorker(requestPath, resultPath string) error {
	data, err := os.ReadFile(requestPath)
	if err != nil {
		return err
	}
	var request captureRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return fmt.Errorf("读取 MuMu 截图请求: %w", err)
	}
	if request.ID == "" {
		return fmt.Errorf("MuMu 截图请求缺少 ID")
	}
	response := captureResponse{ID: request.ID}
	var client *Client
	if request.Manual || request.Options.Manual {
		client, err = Open(request.Options)
	} else {
		client, err = OpenAuto(request.Options)
	}
	if err == nil {
		response.Source = client.Source()
		defer client.Close()
		var img *image.RGBA
		img, err = client.Capture()
		if err == nil {
			// This is SDK acquisition completion, before PNG encoding and helper
			// exit introduce transport latency.
			response.CapturedAt = time.Now()
			response.PNG, err = encodePNG(img)
		}
	}
	if err != nil {
		response.Error = err.Error()
	}
	data, err = encodeCaptureResponse(response)
	if err != nil {
		return err
	}
	return os.WriteFile(resultPath, data, 0o600)
}
