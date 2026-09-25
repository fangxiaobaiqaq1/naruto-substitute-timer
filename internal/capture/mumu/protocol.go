package mumu

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"time"
)

const (
	maxCaptureTimestampFuture = 5 * time.Second
	maxCaptureTimestampAge    = 24 * time.Hour
)

type captureRequest struct {
	ID      string  `json:"id"`
	Options Options `json:"options"`
	Manual  bool    `json:"manual"`
}

type captureResponse struct {
	ID         string    `json:"id"`
	Source     string    `json:"source,omitempty"`
	Error      string    `json:"error,omitempty"`
	CapturedAt time.Time `json:"captured_at,omitempty"`
	PNG        []byte    `json:"png,omitempty"`
}

// WorkerError is a structured SDK failure reported by an otherwise healthy
// helper process. It is distinct from a malformed helper protocol.
type WorkerError struct{ Message string }

func (e *WorkerError) Error() string { return e.Message }

func newCaptureRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate MuMu capture request ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func MarshalCaptureRequest(options Options, manual bool) ([]byte, string, error) {
	id, err := newCaptureRequestID()
	if err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(captureRequest{ID: id, Options: options, Manual: manual})
	return data, id, err
}

func UnmarshalCaptureResponse(data []byte, requestID string) (*image.RGBA, string, time.Time, error) {
	var response captureResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, "", time.Time{}, fmt.Errorf("decode MuMu capture response: %w", err)
	}
	if requestID == "" || response.ID != requestID {
		return nil, "", time.Time{}, fmt.Errorf("MuMu capture helper response request ID mismatch")
	}
	if response.Error != "" {
		return nil, response.Source, time.Time{}, &WorkerError{Message: response.Error}
	}
	if response.Source == "" {
		return nil, "", time.Time{}, fmt.Errorf("MuMu capture helper returned no source")
	}
	if err := validateCaptureTimestamp(response.CapturedAt, time.Now()); err != nil {
		return nil, response.Source, time.Time{}, err
	}
	if len(response.PNG) == 0 {
		return nil, response.Source, time.Time{}, fmt.Errorf("MuMu capture helper returned no pixels")
	}
	decoded, err := png.Decode(bytes.NewReader(response.PNG))
	if err != nil {
		return nil, response.Source, time.Time{}, fmt.Errorf("decode MuMu helper pixels: %w", err)
	}
	bounds := decoded.Bounds()
	if bounds.Dx() < 1 || bounds.Dy() < 1 || bounds.Dx() > 8192 || bounds.Dy() > 8192 {
		return nil, response.Source, time.Time{}, fmt.Errorf("invalid MuMu helper image size %dx%d", bounds.Dx(), bounds.Dy())
	}
	img := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			img.Set(x, y, decoded.At(x, y))
		}
	}
	return img, response.Source, response.CapturedAt, nil
}

func validateCaptureTimestamp(captured, now time.Time) error {
	if captured.IsZero() {
		return fmt.Errorf("MuMu capture helper returned a zero acquisition timestamp")
	}
	if captured.Before(now.Add(-maxCaptureTimestampAge)) {
		return fmt.Errorf("MuMu capture helper returned an unreasonable acquisition timestamp")
	}
	if captured.After(now.Add(maxCaptureTimestampFuture)) {
		return fmt.Errorf("MuMu capture helper returned a future acquisition timestamp")
	}
	return nil
}

func encodeCaptureResponse(response captureResponse) ([]byte, error) {
	return json.Marshal(response)
}

func encodePNG(img *image.RGBA) ([]byte, error) {
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, img); err != nil {
		return nil, err
	}
	return pixels.Bytes(), nil
}
