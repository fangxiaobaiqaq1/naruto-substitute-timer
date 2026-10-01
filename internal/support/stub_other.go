//go:build !(windows && amd64)

package support

import (
	"context"
	"errors"

	"narutotimer/internal/capture/mumu"
)

var errUnsupported = errors.New("支持诊断包与 MuMu SDK 自检仅支持 Windows x64")

func ProbeSDK(context.Context, string, mumu.Options) (mumu.ProbeResult, error) {
	return mumu.ProbeResult{}, errUnsupported
}

func ExportBundle(context.Context, BundleOptions) (string, error) { return "", errUnsupported }
