package layout

import (
	"fmt"
	"image"
	"math"
)

type Mode string

const (
	Auto      Mode = "auto"
	Stretch   Mode = "stretch"
	Letterbox Mode = "letterbox"
)

type Transform struct {
	Client    image.Rectangle
	Content   image.Rectangle
	Capture   image.Rectangle
	Reference image.Point
}

func New(client, capture image.Rectangle, reference image.Point, mode Mode, aspectTolerance float64) (Transform, error) {
	if client.Empty() || capture.Empty() {
		return Transform{}, fmt.Errorf("client and capture rectangles must not be empty")
	}
	if reference.X <= 0 || reference.Y <= 0 {
		return Transform{}, fmt.Errorf("reference dimensions must be positive")
	}
	if !capture.In(client) {
		return Transform{}, fmt.Errorf("capture rectangle must be inside client rectangle")
	}
	if mode != Auto && mode != Stretch && mode != Letterbox {
		return Transform{}, fmt.Errorf("unsupported mode %q", mode)
	}
	content := client
	letterbox := mode == Letterbox
	if mode == Auto {
		target := float64(reference.X) / float64(reference.Y)
		ratio := float64(client.Dx()) / float64(client.Dy())
		letterbox = math.Abs(ratio/target-1) > aspectTolerance
	}
	if letterbox {
		sx := float64(client.Dx()) / float64(reference.X)
		sy := float64(client.Dy()) / float64(reference.Y)
		scale := math.Min(sx, sy)
		w := int(math.Floor(float64(reference.X) * scale))
		h := int(math.Floor(float64(reference.Y) * scale))
		x := client.Min.X + (client.Dx()-w)/2
		y := client.Min.Y + (client.Dy()-h)/2
		content = image.Rect(x, y, x+w, y+h)
	}
	return Transform{Client: client, Content: content, Capture: capture, Reference: reference}, nil
}

func (t Transform) ReferenceToClient(p image.Point) image.Point {
	return image.Pt(
		t.Content.Min.X+int(math.Round(float64(p.X)*float64(t.Content.Dx())/float64(t.Reference.X))),
		t.Content.Min.Y+int(math.Round(float64(p.Y)*float64(t.Content.Dy())/float64(t.Reference.Y))),
	)
}

func (t Transform) ClientToReference(p image.Point) image.Point {
	return image.Pt(
		int(math.Round(float64(p.X-t.Content.Min.X)*float64(t.Reference.X)/float64(t.Content.Dx()))),
		int(math.Round(float64(p.Y-t.Content.Min.Y)*float64(t.Reference.Y)/float64(t.Content.Dy()))),
	)
}

func (t Transform) ClientToCapture(p image.Point) image.Point {
	return p.Sub(t.Capture.Min)
}

func (t Transform) CaptureToClient(p image.Point) image.Point {
	return p.Add(t.Capture.Min)
}

func (t Transform) ReferenceToCapture(p image.Point) image.Point {
	return t.ClientToCapture(t.ReferenceToClient(p))
}

func (t Transform) NormalizedContentToCapture(x, y float64) image.Point {
	client := image.Pt(
		t.Content.Min.X+int(math.Round(x*float64(t.Content.Dx()))),
		t.Content.Min.Y+int(math.Round(y*float64(t.Content.Dy()))),
	)
	return t.ClientToCapture(client)
}
