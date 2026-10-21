// Package imgsafe decodes images from untrusted or semi-trusted sources (ADB
// screenshots, user-supplied template files) only after their header-declared
// dimensions passed a limit. A ~70-byte PNG can declare 65535×65535 RGBA16 and
// make image/png allocate tens of gigabytes before any caller-side size check;
// that is a fatal out-of-memory, not a recoverable panic.
//
// Like image.Decode it only knows the formats registered by the caller's
// imports (image/png, image/jpeg, ...). Valid images decode to exactly the same
// pixels as image.Decode.
package imgsafe

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os"
	"slices"
)

// Limits bounds the declared size of an image before it is decoded.
type Limits struct {
	MaxWidth, MaxHeight int
	MaxPixels           int64
	// Formats, when non-empty, lists the accepted image.RegisterFormat names
	// (for example "png"); anything else is rejected before decoding.
	Formats []string
}

// Capture bounds a full emulator screenshot (same limits as the MuMu SDK and
// raw ADB paths: ≤8192 px per side and ≤16 Mpx).
var Capture = Limits{MaxWidth: 8192, MaxHeight: 8192, MaxPixels: 1 << 24}

// Asset bounds template/crop files loaded from disk; real ones are far smaller.
var Asset = Limits{MaxWidth: 8192, MaxHeight: 8192, MaxPixels: 1 << 24}

// Check validates a decoded header.
func (l Limits) Check(cfg image.Config, format string) error {
	if len(l.Formats) > 0 && !slices.Contains(l.Formats, format) {
		return fmt.Errorf("unsupported image format %q", format)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > l.MaxWidth || cfg.Height > l.MaxHeight || int64(cfg.Width)*int64(cfg.Height) > l.MaxPixels {
		return fmt.Errorf("image dimensions %dx%d exceed the %dx%d / %d px limit", cfg.Width, cfg.Height, l.MaxWidth, l.MaxHeight, l.MaxPixels)
	}
	return nil
}

// DecodeBytes decodes data after checking its header against l.
func DecodeBytes(data []byte, l Limits) (image.Image, string, error) {
	return Decode(bytes.NewReader(data), l)
}

// Decode reads the header with image.DecodeConfig, checks it, rewinds and only
// then decodes the pixels.
func Decode(r io.ReadSeeker, l Limits) (image.Image, string, error) {
	start, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, "", err
	}
	cfg, format, err := image.DecodeConfig(r)
	if err != nil {
		return nil, "", err
	}
	if err := l.Check(cfg, format); err != nil {
		return nil, format, err
	}
	if _, err := r.Seek(start, io.SeekStart); err != nil {
		return nil, format, err
	}
	img, decodedFormat, err := image.Decode(r)
	if err != nil {
		return nil, decodedFormat, err
	}
	// The pixel decoder sniffs the format again; never accept a different one.
	if decodedFormat != format {
		return nil, decodedFormat, fmt.Errorf("image format changed between header (%s) and pixels (%s)", format, decodedFormat)
	}
	return img, decodedFormat, nil
}

// DecodeFile opens path and decodes it with Decode.
func DecodeFile(path string, l Limits) (image.Image, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	return Decode(f, l)
}
