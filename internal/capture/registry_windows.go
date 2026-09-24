//go:build windows && amd64

package capture

import (
	"context"
	"fmt"

	"narutotimer/internal/capture/leidian"
	"narutotimer/internal/capture/mumu"
	"narutotimer/internal/config"
)

type mumuProvider struct{}

func (mumuProvider) Name() string { return MethodMuMuSDK }
func (mumuProvider) Open(_ context.Context, cfg config.CaptureConfig) (Client, error) {
	o := cfg.MuMu
	options := mumu.Options{InstallDir: o.InstallDir, DLLPath: o.DLLPath, Instance: o.Instance, DisplayID: o.DisplayID, Package: o.Package}
	manual := o.Selection == "manual" || (o.Selection != "auto" && (o.InstallDir != "" || o.Instance != 0 || o.DLLPath != ""))
	var (
		client *mumu.Client
		err    error
	)
	if manual {
		client, err = mumu.Open(options)
	} else {
		client, err = mumu.OpenAuto(options)
	}
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("MuMu provider returned an empty client")
	}
	return client, nil
}

type leidianProvider struct{}

func (leidianProvider) Name() string { return MethodLeidianADB }
func (leidianProvider) Open(_ context.Context, cfg config.CaptureConfig) (Client, error) {
	o := cfg.Leidian
	options := leidian.Options{InstallDir: o.InstallDir, ConsolePath: o.ConsolePath, ADBPath: o.ADBPath, Index: o.Index, Serial: o.Serial, Package: o.Package, Connect: o.ConnectOnStart}
	manual := o.Selection == "manual" || (o.Selection != "auto" && (o.InstallDir != "" || o.ConsolePath != "" || o.ADBPath != "" || o.Index != 0 || o.Serial != ""))
	var (
		client *leidian.Client
		err    error
	)
	if manual {
		client, err = leidian.Open(options)
	} else {
		client, err = leidian.OpenAuto(options)
	}
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("雷电 provider returned an empty client")
	}
	return client, nil
}

func DefaultRegistry() *Registry {
	return NewRegistry(mumuProvider{}, leidianProvider{})
}

func OpenConfigured(ctx context.Context, method string, cfg config.CaptureConfig) (Client, error) {
	if method == MethodPrintWindow {
		return nil, fmt.Errorf("printwindow is not an emulator SDK client")
	}
	return DefaultRegistry().Open(ctx, method, cfg)
}
