//go:build windows

package ocr

import (
	"context"
	"image"
	"io"
	"os/exec"
	"testing"
	"time"
)

type discardInput struct{}

func (discardInput) Write(p []byte) (int, error) { return len(p), nil }
func (discardInput) Close() error                { return nil }

func TestStopKeepsHelperRegisteredUntilReaped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reaped := make(chan struct{})
	p := &processIO{cancel: cancel, in: discardInput{}, done: reaped}
	r := &processRecognizer{proc: p}
	returned := make(chan struct{})
	go func() { r.stop(p); close(returned) }()
	<-ctx.Done()
	r.mu.Lock()
	registered := r.proc == p
	r.mu.Unlock()
	select {
	case <-returned:
		t.Error("stop returned before the helper was reaped")
	default:
	}
	// Always release the fake reaper, including on the failing implementation.
	close(reaped)
	<-returned
	if !registered {
		t.Fatal("helper became invisible to Close before process exit")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc != nil {
		t.Fatal("reaped helper is still registered")
	}
}

func TestOversizeProtocolFailsPromptlyAndReapsHelper(t *testing.T) {
	r := newProcessRecognizer(func(ctx context.Context) *exec.Cmd { return fakeCommand(ctx, "oversize") })
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := r.Read(ctx, image.NewGray(image.Rect(0, 0, 20, 20)))
	if err == nil {
		t.Fatal("oversized protocol response accepted")
	}
	if ctx.Err() != nil {
		t.Fatal("invalid protocol waited for the entire request deadline")
	}
}

var _ io.WriteCloser = discardInput{}

func TestRetryAfterDelayedReaperConsumesNewHandshake(t *testing.T) {
	reaped := make(chan struct{})
	r := newProcessRecognizer(func(ctx context.Context) *exec.Cmd { return fakeCommand(ctx, "ok") })
	r.proc = &processIO{stopping: true, ready: true, done: reaped}
	close(reaped)
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	lines, err := r.Read(ctx, image.NewGray(image.Rect(0, 0, 20, 20)))
	if err != nil || len(lines) != 1 {
		t.Fatalf("delayed reaper prevented a clean retry: %v %v", lines, err)
	}
}
