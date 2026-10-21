package hudtext

import (
	"context"
	"image"
	"strings"
	"testing"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/ocr"
	"narutotimer/internal/safego"
)

type panickingReader struct{}

func (panickingReader) Read(context.Context, image.Image) ([]ocr.Line, error) {
	panic("ocr backend bug")
}
func (panickingReader) Close() error { return nil }

// GE-03: the background OCR worker is a long-lived goroutine on the analysis
// path. A panic there must fail that job like a backend error (status
// "unavailable", retry later), not end the process.
func TestOCRWorkerPanicFailsTheJobNotTheProcess(t *testing.T) {
	safego.SetLog(nil)
	e := New(&fakeEngine{}, config.Default(), panickingReader{}, nil)
	t.Cleanup(func() { e.Close() })
	for generation := uint64(1); generation <= 2; generation++ { // the worker keeps serving
		e.jobs <- job{generation: generation}
		select {
		case done := <-e.results:
			if done.err == nil || !strings.Contains(done.err.Error(), "ocr backend bug") || done.job.generation != generation {
				t.Fatalf("job %d: completion err=%v", generation, done.err)
			}
			e.backendFailed(time.Unix(1, 0), done.err)
			if e.status != "unavailable" || !strings.Contains(e.lastError, "ocr backend bug") {
				t.Fatalf("job %d: status %q %q", generation, e.status, e.lastError)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("job %d: no completion", generation)
		}
	}
}
