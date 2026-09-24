//go:build !windows || !amd64

package capture

import (
	"context"
	"errors"

	"narutotimer/internal/config"
)

func DefaultRegistry() *Registry { return NewRegistry() }
func OpenConfigured(context.Context, string, config.CaptureConfig) (Client, error) {
	return nil, errors.New("emulator capture is only supported on Windows amd64")
}
