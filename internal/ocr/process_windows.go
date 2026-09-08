//go:build windows

package ocr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"os/exec"
	"sync"
	"time"
)

type response struct {
	Ready bool   `json:"ready"`
	ID    uint64 `json:"id"`
	Error string `json:"error"`
	Lines []Line `json:"lines"`
}

type processIO struct {
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	in       io.WriteCloser
	out      <-chan response
	done     <-chan struct{}
	stopping bool // guarded by processRecognizer.mu
	ready    bool // handshake consumed, guarded by processRecognizer.serial
}

type processRecognizer struct {
	serial   sync.Mutex
	mu       sync.Mutex
	launch   func(context.Context) *exec.Cmd
	proc     *processIO
	closed   bool
	sequence uint64
}

func newProcessRecognizer(launch func(context.Context) *exec.Cmd) *processRecognizer {
	return &processRecognizer{launch: launch}
}

func (r *processRecognizer) start() (p *processIO, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("OCR is closed")
	}
	if r.proc != nil {
		if r.proc.stopping {
			select {
			case <-r.proc.done:
				r.proc = nil
			default:
				return nil, errors.New("previous OCR helper is still stopping")
			}
		} else {
			return r.proc, nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := r.launch(ctx)
	cmd.WaitDelay = 500 * time.Millisecond
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		cancel()
		return nil, err
	}
	// The protocol reports a bounded user-safe error on stdout. Do not retain
	// unlimited PowerShell diagnostics or screenshot content in stderr buffers.
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		cancel()
		return nil, err
	}
	messages := make(chan response, 1)
	done := make(chan struct{})
	p = &processIO{cmd: cmd, cancel: cancel, in: in, out: messages, done: done}
	r.proc = p
	go func() {
		defer close(done)
		defer close(messages)
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var msg response
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				cancel()
				break
			}
			select {
			case messages <- msg:
			case <-ctx.Done():
			}
			if ctx.Err() != nil {
				break
			}
		}
		// EOF, an overlong line or a broken protocol must also terminate the
		// helper. Waiting first would hang if it stopped writing but stayed alive.
		cancel()
		// Stdout is drained before Wait; no concurrent Wait may close it early.
		cmd.Wait()
	}()
	return p, nil
}

func receive(ctx context.Context, p *processIO) (response, error) {
	select {
	case msg, ok := <-p.out:
		if !ok {
			return response{}, errors.New("system OCR helper exited")
		}
		if msg.Error != "" {
			return msg, fmt.Errorf("system OCR: %.400s", msg.Error)
		}
		return msg, nil
	case <-ctx.Done():
		return response{}, ctx.Err()
	}
}

func (r *processRecognizer) Read(ctx context.Context, img image.Image) (lines []Line, err error) {
	r.serial.Lock()
	defer r.serial.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return nil, errors.New("OCR is closed")
	}
	data, err := encodeImage(img)
	if err != nil {
		return nil, err
	}
	p, err := r.start()
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			err = errors.Join(err, r.stop(p))
		}
	}()
	if !p.ready {
		hello, err := receive(ctx, p)
		if err != nil {
			return nil, err
		}
		if !hello.Ready {
			return nil, errors.New("system OCR unavailable")
		}
		p.ready = true
	}
	r.sequence++
	request := struct {
		ID    uint64 `json:"id"`
		Image string `json:"image"`
	}{r.sequence, data}
	// A helper stuck before reading stdin cannot block shutdown/capture: the
	// write happens only here, with cancellation killing and closing its pipes.
	writeStopped := make(chan struct{})
	cancelWrite := context.AfterFunc(ctx, func() {
		defer close(writeStopped)
		p.cancel()
		p.in.Close()
	})
	err = json.NewEncoder(p.in).Encode(request)
	if !cancelWrite() {
		// Do not let a late cancellation callback kill a reused successful helper.
		<-writeStopped
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	msg, err := receive(ctx, p)
	if err != nil {
		return nil, err
	}
	if msg.ID != r.sequence {
		return nil, errors.New("out-of-order OCR response")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	success = true
	return msg.Lines, nil
}

func (r *processRecognizer) stop(p *processIO) error {
	r.mu.Lock()
	p.stopping = true
	r.mu.Unlock()
	p.cancel()
	p.in.Close()
	// A failed Read must not detach a still-live helper: Close and a retry must
	// see it until the stdout owner has finished Wait and released the executable.
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-p.done:
		r.mu.Lock()
		if r.proc == p {
			r.proc = nil
		}
		r.mu.Unlock()
		return nil
	case <-timer.C:
		return errors.New("OCR helper did not stop in time")
	}
}

func (r *processRecognizer) Close() error {
	r.mu.Lock()
	r.closed = true
	p := r.proc
	r.mu.Unlock()
	if p != nil {
		return r.stop(p)
	}
	return nil
}
