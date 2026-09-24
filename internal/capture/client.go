package capture

import (
	"image"
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
