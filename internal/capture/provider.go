package capture

import (
	"context"
	"fmt"
	"image"
	"time"

	"narutotimer/internal/config"
)

const (
	MethodMuMuSDK     = "mumu-sdk"
	MethodLeidianADB  = "leidian-adb"
	MethodPrintWindow = "printwindow-fullcontent"
	// MethodLeidianADBRaw reads uncompressed screencap over the adb host
	// protocol; MethodLeidianWindow PrintWindows the LDPlayer render window.
	// Both fall back to MethodLeidianADB per frame inside the 雷电 client.
	MethodLeidianADBRaw = "leidian-adb-raw"
	MethodLeidianWindow = "leidian-window"
)

// DefaultLeidianMethods is the 雷电 per-frame chain when
// capture.preferredMethods names no 雷电 method. leidian-window is opt-in:
// its frames come at the window client size, not the device resolution.
func DefaultLeidianMethods() []string {
	return []string{MethodLeidianADBRaw, MethodLeidianADB}
}

// LeidianMethodChain returns the ordered 雷电 capture methods taken from
// cfg.PreferredMethods (non-雷电 entries are skipped, duplicates dropped).
// When none are listed it returns DefaultLeidianMethods. MuMu is unaffected.
func LeidianMethodChain(cfg config.CaptureConfig) []string {
	var chain []string
	seen := map[string]bool{}
	for _, method := range cfg.PreferredMethods {
		switch method {
		case MethodLeidianWindow, MethodLeidianADBRaw, MethodLeidianADB:
			if !seen[method] {
				seen[method] = true
				chain = append(chain, method)
			}
		}
	}
	if len(chain) == 0 {
		return DefaultLeidianMethods()
	}
	return chain
}

// LeidianCaptureTimeout derives the per-frame command timeout from
// capture.timeoutMs: slightly below the frame budget, never under 300 ms.
// Setup commands and the adb exec-out PNG path keep their own 5 s default.
func LeidianCaptureTimeout(timeoutMS int) time.Duration {
	timeout := time.Duration(timeoutMS)*time.Millisecond - 100*time.Millisecond
	if timeout < 300*time.Millisecond {
		timeout = 300 * time.Millisecond
	}
	return timeout
}

type Provider interface {
	Name() string
	Open(ctx context.Context, cfg config.CaptureConfig) (Client, error)
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	r := &Registry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		if provider != nil {
			r.providers[provider.Name()] = provider
		}
	}
	return r
}

func (r *Registry) Open(ctx context.Context, method string, cfg config.CaptureConfig) (Client, error) {
	provider, ok := r.providers[method]
	if !ok {
		return nil, fmt.Errorf("unsupported capture method %q", method)
	}
	return provider.Open(ctx, cfg)
}

type StaticClient struct {
	CaptureFunc func() (*image.RGBA, error)
	SourceName  string
}

func (c StaticClient) Capture() (*image.RGBA, error) {
	if c.CaptureFunc == nil {
		return nil, fmt.Errorf("capture function is nil")
	}
	return c.CaptureFunc()
}
func (c StaticClient) Close() error   { return nil }
func (c StaticClient) Source() string { return c.SourceName }
