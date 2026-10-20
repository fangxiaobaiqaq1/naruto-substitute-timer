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

// RowHashClient is an optional Client extension: it captures like Capture and
// also returns hash(row) for every row of the returned image, top to bottom,
// computed while each row is still in cache. The frame scheduler folds these
// into its duplicate-frame fingerprint instead of re-reading the whole image.
// hashes is a reusable buffer; nil hash skips hashing.
type RowHashClient interface {
	CaptureRowHashes(hash func([]byte) uint64, hashes []uint64) (*image.RGBA, []uint64, error)
}
