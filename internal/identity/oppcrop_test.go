package identity

import (
	"image"
	"testing"
	"time"

	"narutotimer/internal/detect"
)

// The opponent crop is copied synchronously: the capture buffer it came from
// may be reused by the next capture while the PNG is still being written.
func TestOppCropIsAnOwnedCopyAndRateLimited(t *testing.T) {
	saveMu.Lock()
	saved := lastSave
	lastSave = time.Time{}
	saveMu.Unlock()
	defer func() {
		saveMu.Lock()
		lastSave = saved
		saveMu.Unlock()
	}()
	img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7)
	}
	crop := oppCrop(img, "right", detect.ModeStretch)
	if crop == nil || crop.Bounds().Dx() < 8 {
		t.Fatalf("crop %v", crop)
	}
	before := append([]byte(nil), crop.Pix...)
	for i := range img.Pix {
		img.Pix[i] = 0
	}
	if string(before) != string(crop.Pix) {
		t.Fatal("crop aliases the capture buffer")
	}
	saveMu.Lock()
	lastSave = time.Now()
	saveMu.Unlock()
	if oppCrop(img, "right", detect.ModeStretch) != nil {
		t.Fatal("crop taken although no save is due")
	}
}
