package ocr

import (
	"bufio"
	"context"
	"encoding/json"
	"image"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestOCRHelperProcess(t *testing.T) {
	mode := os.Getenv("TIMER_OCR_HELPER_MODE")
	if mode == "" {
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.Encode(response{Ready: true})
	if mode == "never-read" {
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 4<<20)
	for scan.Scan() {
		var q struct {
			ID uint64 `json:"id"`
		}
		json.Unmarshal(scan.Bytes(), &q)
		if mode == "hang" {
			time.Sleep(10 * time.Second)
			continue
		}
		if mode == "exit" {
			os.Exit(1)
		}
		if mode == "oversize" {
			os.Stdout.WriteString(strings.Repeat("x", (1<<20)+1) + "\n")
			time.Sleep(10 * time.Second)
			continue
		}
		id := q.ID
		if mode == "wrong-id" {
			id++
		}
		enc.Encode(response{ID: id, Lines: []Line{{Text: "已识别"}}})
	}
	os.Exit(0)
}

func fakeCommand(ctx context.Context, mode string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOCRHelperProcess$")
	cmd.Env = append(os.Environ(), "TIMER_OCR_HELPER_MODE="+mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd
}

func TestProcessTimeoutKillsHelperAndNextReadStartsClean(t *testing.T) {
	var count atomic.Int32
	r := newProcessRecognizer(func(ctx context.Context) *exec.Cmd {
		mode := "ok"
		if count.Add(1) == 1 {
			mode = "hang"
		}
		return fakeCommand(ctx, mode)
	})
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	img := image.NewGray(image.Rect(0, 0, 100, 30))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	if _, err := r.Read(ctx, img); err == nil {
		t.Fatal("hung request did not time out")
	}
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	lines, err := r.Read(ctx, img)
	if err != nil || len(lines) != 1 || count.Load() != 2 {
		t.Fatalf("retry reused old process/output: %v %+v launches=%d", err, lines, count.Load())
	}
}

func TestProcessRejectsWrongResponseID(t *testing.T) {
	r := newProcessRecognizer(func(ctx context.Context) *exec.Cmd { return fakeCommand(ctx, "wrong-id") })
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := r.Read(ctx, image.NewGray(image.Rect(0, 0, 20, 20))); err == nil {
		t.Fatal("response from another request accepted")
	}
}

func TestCloseCancelsInFlightProcessRead(t *testing.T) {
	r := newProcessRecognizer(func(ctx context.Context) *exec.Cmd { return fakeCommand(ctx, "hang") })
	done := make(chan error, 1)
	go func() { _, err := r.Read(context.Background(), image.NewGray(image.Rect(0, 0, 20, 20))); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		r.mu.Lock()
		started := r.proc != nil
		r.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read succeeded after close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read blocked after Close")
	}
	if _, err := r.Read(context.Background(), image.NewGray(image.Rect(0, 0, 20, 20))); err == nil {
		t.Fatal("closed recognizer restarted")
	}
}

func TestProcessCancellationUnblocksFullInputPipe(t *testing.T) {
	r := newProcessRecognizer(func(ctx context.Context) *exec.Cmd { return fakeCommand(ctx, "never-read") })
	defer r.Close()
	img := image.NewGray(image.Rect(0, 0, 700, 700))
	state := uint32(0x981abc32)
	for i := range img.Pix {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		img.Pix[i] = uint8(state)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := r.Read(ctx, img); err == nil {
		t.Fatal("blocked pipe did not honor cancellation")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("blocked stdin survived context deadline")
	}
}
