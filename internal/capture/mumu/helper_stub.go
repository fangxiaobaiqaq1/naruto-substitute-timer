//go:build !windows || !amd64

package mumu

import "errors"

func RunCaptureWorker(string, string) error {
	return errors.New("MuMu capture helper is only supported on Windows amd64")
}
