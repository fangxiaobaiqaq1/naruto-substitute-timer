package capture

import (
	"context"
	"image"
	"time"

	"narutotimer/internal/capture/helper"
)

// Client is the provider-neutral boundary used by the frame scheduler.
// Implementations must return an owned, top-down RGBA image. Capture time is
// the time the provider finished copying pixels; it is not an Android render
// timestamp.
type Client interface {
	Capture() (*image.RGBA, error)
	Close() error
	Source() string
}

// ContextClient is implemented when capture can be terminated with the caller's
// deadline. In-process vendor clients intentionally do not implement it.
type ContextClient interface {
	Client
	CaptureContext(context.Context) (*image.RGBA, time.Time, error)
}

// CloseWaitClient marks a client whose Close synchronously cancels and reaps an
// external capture helper. Legacy in-process SDK clients intentionally do not
// implement it because a shutdown wait could be unbounded.
type CloseWaitClient interface {
	Client
	CloseWaitsForCapture() bool
}

// HelperTransactionClient exposes only non-sensitive isolated-helper metadata
// for correlation in frame diagnostics.
type HelperTransactionClient interface {
	Client
	HelperTransaction() helper.Transaction
}
