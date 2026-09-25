// Package helper provides a short-lived process boundary for capture adapters
// whose vendor calls cannot be cancelled safely in-process.
package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Lifecycle owns the currently running helper. Close cancels it and waits until
// CommandContext has killed/reaped the child and Run has removed its temp files.
var (
	ErrLifecycleClosed = errors.New("capture helper lifecycle is closed")
	ErrLifecycleActive = errors.New("capture helper lifecycle already has an active helper")
)

const (
	OutcomeBusy           = "busy"
	OutcomeTimeout        = "timeout"
	OutcomeHelperLaunch   = "helper_launch"
	OutcomeHelperProtocol = "helper_protocol"
	OutcomeSDKWorker      = "sdk_worker"
	OutcomeSuccess        = "success"

	ReapNotNeeded    = "not_needed"
	ReapCompleted    = "completed"
	ReapKilledReaped = "killed_reaped"
	ReapPending      = "pending"
)

// Transaction contains correlated, non-sensitive helper lifecycle metadata.
// It deliberately excludes executable paths, request IDs, arguments and pixels.
type Transaction struct {
	RequestStarted time.Time
	Deadline       time.Time
	Outcome        string
	Reap           string
}

// ClassifiedError lets protocol adapters retain the underlying error while
// assigning a stable diagnostic outcome.
type ClassifiedError struct {
	Outcome string
	Err     error
}

func (e *ClassifiedError) Error() string { return e.Err.Error() }
func (e *ClassifiedError) Unwrap() error { return e.Err }

func Classify(err error) string {
	var classified *ClassifiedError
	if errors.As(err, &classified) {
		return classified.Outcome
	}
	return ""
}

type Lifecycle struct {
	mu     sync.Mutex
	closed bool
	cancel context.CancelFunc
	done   chan struct{}
}

func (l *Lifecycle) start(cancel context.CancelFunc) (<-chan struct{}, error) {
	if l == nil {
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrLifecycleClosed
	}
	if l.done != nil {
		return nil, ErrLifecycleActive
	}
	l.cancel = cancel
	l.done = make(chan struct{})
	return l.done, nil
}

func (l *Lifecycle) finish(done <-chan struct{}) {
	if l == nil || done == nil {
		return
	}
	l.mu.Lock()
	if l.done == done {
		close(l.done)
		l.done, l.cancel = nil, nil
	}
	l.mu.Unlock()
}

// Close is idempotent. It applies only to the helper-backed client owning this
// lifecycle; legacy in-process capture never uses it and therefore never gains
// an unbounded shutdown wait.
// Cancel signals an active helper without waiting for its process reap. It is
// used by source reconfiguration so a synchronous settings action cannot block
// on a vendor process. Close remains the shutdown/reap operation.
func (l *Lifecycle) Cancel() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	cancel := l.cancel
	l.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (l *Lifecycle) Close() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	l.closed = true
	cancel, done := l.cancel, l.done
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
		return true
	}
	return false
}

// Run writes request to a private temporary directory, starts executable with
// argument plus request/result paths, and waits for it to exit. CommandContext
// kills the helper on cancellation; CombinedOutput waits for that killed helper
// to be reaped before Run returns, so a following request cannot overlap it.
func Run(ctx context.Context, executable, argument string, request []byte, extraArgs ...string) ([]byte, error) {
	return RunWithLifecycle(ctx, nil, executable, argument, request, extraArgs...)
}

// RunWithLifecycle additionally lets the owning runtime provider synchronously
// cancel and reap an active helper during shutdown.
func RunWithLifecycle(ctx context.Context, lifecycle *Lifecycle, executable, argument string, request []byte, extraArgs ...string) ([]byte, error) {
	result, _, err := RunWithLifecycleTransaction(ctx, lifecycle, executable, argument, request, extraArgs...)
	return result, err
}

// RunWithLifecycleTransaction is RunWithLifecycle with lifecycle-only metadata
// for support diagnostics. It always preserves the original failure as Err.
func RunWithLifecycleTransaction(ctx context.Context, lifecycle *Lifecycle, executable, argument string, request []byte, extraArgs ...string) ([]byte, Transaction, error) {
	transaction := Transaction{RequestStarted: time.Now(), Outcome: OutcomeHelperLaunch, Reap: ReapNotNeeded}
	if deadline, ok := ctx.Deadline(); ok {
		transaction.Deadline = deadline
	}
	if executable == "" {
		return nil, transaction, fmt.Errorf("capture helper executable is empty")
	}
	ctx, cancel := context.WithCancel(ctx)
	done, err := lifecycle.start(cancel)
	if err != nil {
		cancel()
		return nil, transaction, err
	}
	defer func() {
		cancel()
		lifecycle.finish(done)
	}()
	dir, err := os.MkdirTemp("", "naruto-timer-capture-")
	if err != nil {
		return nil, transaction, err
	}
	defer os.RemoveAll(dir)
	requestPath := filepath.Join(dir, "request.bin")
	resultPath := filepath.Join(dir, "result.bin")
	if err := os.WriteFile(requestPath, request, 0o600); err != nil {
		return nil, transaction, err
	}
	args := make([]string, 0, 3+len(extraArgs))
	args = append(args, argument, requestPath, resultPath)
	args = append(args, extraArgs...)
	cmd := exec.CommandContext(ctx, executable, args...)
	output, runErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			transaction.Outcome = OutcomeTimeout
		} else {
			transaction.Outcome = OutcomeSDKWorker
		}
		transaction.Reap = ReapKilledReaped
		return nil, transaction, fmt.Errorf("capture helper terminated: %w", ctx.Err())
	}
	// Never accept result data from a non-zero/crashed worker. Structured SDK
	// failures are written with a successful process exit and Error in protocol.
	if runErr != nil {
		transaction.Outcome = OutcomeSDKWorker
		if _, ok := runErr.(*exec.Error); ok {
			transaction.Outcome = OutcomeHelperLaunch
		}
		transaction.Reap = ReapCompleted
		// Helper stdout/stderr is untrusted and may contain paths, request data,
		// tokens, or pixels. The stable transaction outcome is the diagnostic
		// surface; retain no child output in an error that can reach the UI/log.
		_ = output
		return nil, transaction, fmt.Errorf("capture helper failed: %w", runErr)
	}
	result, readErr := os.ReadFile(resultPath)
	transaction.Reap = ReapCompleted
	if readErr == nil {
		transaction.Outcome = OutcomeSuccess
		return result, transaction, nil
	}
	transaction.Outcome = OutcomeHelperProtocol
	return nil, transaction, readErr
}
