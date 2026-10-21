//go:build windows && amd64

// Package support builds a bounded diagnostic ZIP for user-initiated support
// requests. It only includes an allowlisted set of application files.
package support

import "context"

// ExportBundle writes the support ZIP (see exportBundle in bundle.go).
func ExportBundle(ctx context.Context, options BundleOptions) (string, error) {
	return exportBundle(ctx, options)
}
