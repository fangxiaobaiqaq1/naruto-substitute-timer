package mumu

import (
	"image"
	"image/color"
	"testing"
	"time"
)

func testCaptureResponse(t *testing.T, id string, captured time.Time) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	img.SetRGBA(1, 0, color.RGBA{R: 5, G: 6, B: 7, A: 255})
	png, err := encodePNG(img)
	if err != nil {
		t.Fatal(err)
	}
	data, err := encodeCaptureResponse(captureResponse{ID: id, Source: "MuMu test", CapturedAt: captured, PNG: png})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCaptureProtocolRejectsRequestIDMismatch(t *testing.T) {
	if _, _, _, err := UnmarshalCaptureResponse(testCaptureResponse(t, "actual", time.Now()), "expected"); err == nil {
		t.Fatal("accepted a response for another request")
	}
}

func TestCaptureProtocolRejectsInvalidTimestamp(t *testing.T) {
	for _, captured := range []time.Time{time.Time{}, time.Now().Add(6 * time.Second), time.Now().Add(-25 * time.Hour)} {
		if _, _, _, err := UnmarshalCaptureResponse(testCaptureResponse(t, "id", captured), "id"); err == nil {
			t.Fatalf("accepted invalid timestamp %v", captured)
		}
	}
}

func TestCaptureProtocolRoundTripPreservesPixelsAndAcquisitionTime(t *testing.T) {
	captured := time.Now().Add(-time.Millisecond).Round(0)
	img, source, got, err := UnmarshalCaptureResponse(testCaptureResponse(t, "id", captured), "id")
	if err != nil {
		t.Fatal(err)
	}
	if source != "MuMu test" || !got.Equal(captured) {
		t.Fatalf("source/time = %q/%s, want MuMu test/%s", source, got, captured)
	}
	if img.Bounds() != image.Rect(0, 0, 2, 1) || img.RGBAAt(0, 0).R != 1 || img.RGBAAt(1, 0).B != 7 {
		t.Fatalf("PNG round trip changed pixels: %+v", img)
	}
}
