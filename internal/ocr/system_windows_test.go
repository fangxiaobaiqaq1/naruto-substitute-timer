package ocr

import (
	"context"
	"image"
	_ "image/png"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSystemOCRWithInstalledChineseLanguage(t *testing.T) {
	if os.Getenv("TIMER_TEST_SYSTEM_OCR") != "1" {
		t.Skip("opt in: uses the installed Windows Chinese OCR")
	}
	path := os.Getenv("TIMER_OCR_TEST_IMAGE")
	if path == "" {
		t.Fatal("TIMER_OCR_TEST_IMAGE is required")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	reader := NewSystem()
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	})
	for i := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := time.Now()
		lines, err := reader.Read(ctx, img)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for _, line := range lines {
			text.WriteString(line.Text)
		}
		compact := strings.ReplaceAll(text.String(), " ", "")
		t.Logf("request %d took %v: %s", i, time.Since(start), compact)
		if !strings.Contains(compact, "山中井野") || !strings.Contains(compact, "白方小") {
			t.Fatalf("recorded name not read: %q", compact)
		}
	}
}
