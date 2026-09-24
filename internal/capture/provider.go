package capture

import (
	"context"
	"fmt"
	"image"

	"narutotimer/internal/config"
)

const (
	MethodMuMuSDK     = "mumu-sdk"
	MethodLeidianADB  = "leidian-adb"
	MethodPrintWindow = "printwindow-fullcontent"
)

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
