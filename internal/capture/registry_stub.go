//go:build !windows || !amd64

package capture

import (
	"context"
	"errors"

	"narutotimer/internal/capture/helper"
	"narutotimer/internal/config"
)

func DefaultRegistry() *Registry { return NewRegistry() }
func OpenConfigured(context.Context, string, config.CaptureConfig) (Client, error) {
	return nil, errors.New("emulator capture is only supported on Windows amd64")
}
func OpenRuntimeMuMu(string, config.CaptureConfig, *helper.Lifecycle) (Client, error) {
	return nil, errors.New("MuMu runtime helper is only supported on Windows amd64")
}
