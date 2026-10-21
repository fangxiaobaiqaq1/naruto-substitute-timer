//go:build ocrhost && !windows

package ocr

// Offline host harness only (build tag ocrhost, never in a release): runs the
// Windows helper's request path (local_rows.go) in-process on Linux with an
// ONNX Runtime shared library (e.g. libonnxruntime.so.1.27.0 from the official
// onnxruntime 1.27.0 PyPI wheel, the version the Windows build embeds), so
// android/tools/ocrcheck can show what the Windows OCR path reads on a frame.

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

type hostRecognizer struct {
	mu         sync.Mutex
	session    *ort.DynamicAdvancedSession
	characters []string
}

// NewHost opens model with the ONNX Runtime library at library, two intra-op
// threads like the Windows helper.
func NewHost(library string, model []byte) (Recognizer, error) {
	ort.SetSharedLibraryPath(library)
	if err := ort.InitializeEnvironment(); err != nil {
		return nil, fmt.Errorf("ONNX environment: %w", err)
	}
	options, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer options.Destroy()
	if err := options.SetIntraOpNumThreads(2); err != nil {
		return nil, err
	}
	if err := options.SetInterOpNumThreads(1); err != nil {
		return nil, err
	}
	session, err := ort.NewDynamicAdvancedSessionWithONNXData(model, []string{"x"}, []string{"softmax_11.tmp_0"}, options)
	if err != nil {
		return nil, err
	}
	characters, err := sessionCharacters(session)
	if err != nil {
		session.Destroy()
		return nil, err
	}
	return &hostRecognizer{session: session, characters: characters}, nil
}

func (*hostRecognizer) PrefersColorRows() bool { return true }

func (h *hostRecognizer) Read(ctx context.Context, img image.Image) ([]Line, error) {
	return h.ReadRegions(ctx, img, nil)
}

// ReadRegions sends the image through the same PNG/base64 request the Windows
// helper decodes, so the row pixels and types match the helper's exactly.
func (h *hostRecognizer) ReadRegions(ctx context.Context, img image.Image, regions []image.Rectangle) ([]Line, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, img); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.session == nil {
		return nil, errors.New("OCR is closed")
	}
	return recognizeRequest(h.session, h.characters, base64.StdEncoding.EncodeToString(out.Bytes()), regions)
}

func (h *hostRecognizer) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.session != nil {
		h.session.Destroy()
		h.session = nil
	}
	return nil
}
