package ninja

import (
	"bytes"
	"image"
	"image/draw"
	"narutotimer/internal/match"
	"testing"
	"time"
)

func TestStudentNameIsDisplayOnlyAndRejectsOcclusion(t *testing.T) {
	data, err := templates.ReadFile("templates/naruto_student.png")
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{800, 950, 1280, 1920} {
		scale := float64(width) / 960
		gray := match.ToGray(src)
		part := match.ScaleGray(gray, int(float64(gray.Bounds().Dx())*scale+.5), int(float64(gray.Bounds().Dy())*scale+.5))
		for _, left := range []bool{true, false} {
			first := image.Pt(int(106*scale), int(61*scale))
			if !left {
				first.X = int(854 * scale)
			}
			roi := NameRegion(first, scale, left)
			frame := image.NewRGBA(image.Rect(0, 0, width, width*9/16))
			at := image.Pt(roi.Min.X+int(12*scale), roi.Min.Y+int(7*scale))
			box := image.Rectangle{Min: at, Max: at.Add(part.Bounds().Size())}
			draw.Draw(frame, box, part, image.Point{}, draw.Src)
			reader := NewReader()
			var tracker Tracker
			now := time.Unix(100, 0)
			got := tracker.Read(reader, frame, roi, scale, now)
			if got.Name != NarutoStudent || got.Slots != 0 || got.Palette != "" {
				t.Fatalf("width=%d left=%v: %+v", width, left, got)
			}
			// Concealing the version must not inherit either this full name or sixth-tail rules.
			draw.Draw(frame, image.Rect(box.Min.X+box.Dx()/2, box.Min.Y, box.Max.X, box.Max.Y), image.Black, image.Point{}, draw.Src)
			if got := tracker.Read(reader, frame, roi, scale, now.Add(time.Millisecond)); got.Name != "" || got.Slots != 0 {
				t.Fatalf("occluded: %+v", got)
			}
		}
	}
}
