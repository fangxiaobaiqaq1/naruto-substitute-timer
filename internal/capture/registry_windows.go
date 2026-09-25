//go:build windows && amd64

package capture

import (
	"context"
	"errors"
	"fmt"
	"image"
	"sync"
	"time"

	"narutotimer/internal/capture/helper"
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

// OpenRuntimeMuMu returns a client backed by one short-lived helper process per
// capture. The native SDK has no cancellation API, so the parent never keeps a
// potentially stuck DLL/client alive after a timeout.
func OpenRuntimeMuMu(executable string, cfg config.CaptureConfig, lifecycle *helper.Lifecycle) (Client, error) {
	if executable == "" {
		return nil, fmt.Errorf("无法定位计时器可执行文件")
	}
	o := cfg.MuMu
	manual := o.Selection == "manual" || (o.Selection != "auto" && (o.InstallDir != "" || o.Instance != 0 || o.DLLPath != ""))
	return &runtimeMuMuClient{executable: executable, options: mumu.Options{InstallDir: o.InstallDir, DLLPath: o.DLLPath, Instance: o.Instance, DisplayID: o.DisplayID, Package: o.Package, Manual: manual}, manual: manual, lifecycle: lifecycle}, nil
}

type runtimeMuMuClient struct {
	executable string
	options    mumu.Options
	manual     bool
	mu         sync.RWMutex
	source     string
	lifecycle  *helper.Lifecycle
	last       helper.Transaction
}

func (c *runtimeMuMuClient) Capture() (*image.RGBA, error) {
	img, _, err := c.CaptureContext(context.Background())
	return img, err
}

func (c *runtimeMuMuClient) CaptureContext(ctx context.Context) (*image.RGBA, time.Time, error) {
	request, requestID, err := mumu.MarshalCaptureRequest(c.options, c.manual)
	if err != nil {
		c.setTransaction(helper.Transaction{RequestStarted: time.Now(), Outcome: helper.OutcomeHelperProtocol, Reap: helper.ReapNotNeeded})
		return nil, time.Time{}, &helper.ClassifiedError{Outcome: helper.OutcomeHelperProtocol, Err: err}
	}
	data, transaction, err := helper.RunWithLifecycleTransaction(ctx, c.lifecycle, c.executable, mumu.CaptureWorkerArgument, request)
	if err != nil {
		c.setTransaction(transaction)
		return nil, time.Time{}, err
	}
	img, source, capturedAt, err := mumu.UnmarshalCaptureResponse(data, requestID)
	if err != nil {
		var workerErr *mumu.WorkerError
		if errors.As(err, &workerErr) {
			transaction.Outcome = helper.OutcomeSDKWorker
		} else {
			transaction.Outcome = helper.OutcomeHelperProtocol
		}
		c.setTransaction(transaction)
		return nil, time.Time{}, &helper.ClassifiedError{Outcome: transaction.Outcome, Err: err}
	}
	if source != "" {
		c.mu.Lock()
		c.source = source
		c.mu.Unlock()
	}
	c.setTransaction(transaction)
	return img, capturedAt, nil
}

func (c *runtimeMuMuClient) setTransaction(transaction helper.Transaction) {
	c.mu.Lock()
	c.last = transaction
	c.mu.Unlock()
}

// HelperTransaction supplies non-sensitive request lifecycle diagnostics to the
// frame provider. It is safe because the provider permits only one capture.
func (c *runtimeMuMuClient) HelperTransaction() helper.Transaction {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.last
}

// Close has no native resource to release: helper lifecycle cancellation is
// owned by the frame provider so shutdown can signal it while CaptureContext is
// active without self-waiting from the capture worker.
func (c *runtimeMuMuClient) Close() error { return nil }
func (c *runtimeMuMuClient) Source() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.source != "" {
		return c.source
	}
	return fmt.Sprintf("MuMu 实例 %d", c.options.Instance)
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
