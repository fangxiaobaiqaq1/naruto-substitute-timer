package mumu

import "time"

const (
	// HelperTransactionTimeout bounds an isolated SDK operation as a whole:
	// process launch, SDK setup/capture, PNG encoding, and protocol transport.
	// It intentionally matches the explicit MuMu SDK probe budget. Capture
	// TimeoutMS is not a safe deadline for that larger transaction.
	HelperTransactionTimeout = 15 * time.Second

	// ProbeWorkerArgument is handled before timer-app starts OCR or Fyne.
	ProbeWorkerArgument = "--timer-mumu-sdk-probe-v1"
	// CaptureWorkerArgument performs exactly one Open/Capture/Close operation.
	CaptureWorkerArgument = "--timer-mumu-sdk-capture-v1"
)
