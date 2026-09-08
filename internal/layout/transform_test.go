package layout

import (
	"image"
	"testing"
)

func TestTransformLetterboxAndROI(t *testing.T) {
	tr, err := New(image.Rect(0, 0, 1092, 654), image.Rect(0, 0, 1092, 200), image.Pt(1920, 1080), Letterbox, 0.015)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tr.Content, image.Rect(0, 20, 1092, 634); got != want {
		t.Fatalf("content = %v, want %v", got, want)
	}
	client := tr.ReferenceToClient(image.Pt(195, 155))
	if client.X < 110 || client.X > 112 || client.Y < 107 || client.Y > 109 {
		t.Fatalf("mapped bead center = %v", client)
	}
	capture := tr.ReferenceToCapture(image.Pt(195, 155))
	if capture != client {
		t.Fatalf("capture point = %v, want %v", capture, client)
	}
}

func TestTransformNonZeroCaptureOrigin(t *testing.T) {
	tr, err := New(image.Rect(0, 0, 1920, 1080), image.Rect(0, 0, 1920, 300), image.Pt(1920, 1080), Stretch, 0.015)
	if err != nil {
		t.Fatal(err)
	}
	got := tr.ReferenceToCapture(image.Pt(225, 155))
	if got != image.Pt(225, 155) {
		t.Fatalf("reference to capture = %v", got)
	}

	tr, err = New(image.Rect(0, 0, 1920, 1080), image.Rect(100, 50, 900, 350), image.Pt(1920, 1080), Stretch, 0.015)
	if err != nil {
		t.Fatal(err)
	}
	got = tr.ReferenceToCapture(image.Pt(225, 155))
	if got != image.Pt(125, 105) {
		t.Fatalf("reference to offset capture = %v", got)
	}
}

func TestTransformRoundTrip(t *testing.T) {
	tr, err := New(image.Rect(0, 0, 1007, 606), image.Rect(0, 0, 1007, 606), image.Pt(1920, 1080), Letterbox, 0.015)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []image.Point{{0, 0}, {195, 155}, {960, 540}, {1674, 155}, {1919, 1079}} {
		got := tr.ClientToReference(tr.ReferenceToClient(p))
		if abs(got.X-p.X) > 1 || abs(got.Y-p.Y) > 1 {
			t.Fatalf("round trip %v -> %v", p, got)
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
