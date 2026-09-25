package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunAllowsSlowValidHelperWithinTransactionBudget(t *testing.T) {
	t.Setenv("NARUTO_TIMER_HELPER_TEST", "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, transaction, err := RunWithLifecycleTransaction(ctx, nil, os.Args[0], "-test.run=TestCaptureHelperProcess", []byte("fresh"))
	if err != nil || string(data) != "fresh" {
		t.Fatalf("slow helper = %q, %v", data, err)
	}
	if transaction.Outcome != OutcomeSuccess || transaction.Reap != ReapCompleted || transaction.RequestStarted.IsZero() || transaction.Deadline.IsZero() {
		t.Fatalf("slow helper transaction = %+v", transaction)
	}
}

func TestRunTerminatesStalledHelperThenAcceptsFreshResponse(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "first-started")
	t.Setenv("NARUTO_TIMER_HELPER_TEST", marker)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, transaction, err := RunWithLifecycleTransaction(ctx, nil, os.Args[0], "-test.run=TestCaptureHelperProcess", []byte("stale")); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled helper error = %v, want deadline exceeded", err)
	} else if transaction.Outcome != OutcomeTimeout || transaction.Reap != ReapKilledReaped {
		t.Fatalf("stalled helper transaction = %+v", transaction)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("stalled helper never entered: %v", err)
	}

	fresh, err := Run(context.Background(), os.Args[0], "-test.run=TestCaptureHelperProcess", []byte("fresh"))
	if err != nil {
		t.Fatalf("fresh helper did not recover: %v", err)
	}
	if string(fresh) != "fresh" {
		t.Fatalf("fresh helper returned stale result %q", fresh)
	}
}

func TestLifecycleCloseTerminatesAndReapsStalledHelper(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	marker := filepath.Join(root, "started")
	t.Setenv("NARUTO_TIMER_HELPER_TEST", marker)
	var lifecycle Lifecycle
	returned := make(chan error, 1)
	go func() {
		_, err := RunWithLifecycle(context.Background(), &lifecycle, os.Args[0], "-test.run=TestCaptureHelperProcess", []byte("stalled"))
		returned <- err
	}()
	waitForFile(t, marker)

	closed := make(chan struct{})
	go func() { lifecycle.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close did not wait for helper termination/reap")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "started" {
		t.Fatalf("helper lifecycle left temporary artifacts: entries=%v err=%v", entries, err)
	}
	assertNoHelperTempDirs(t, entries)
	select {
	case err := <-returned:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("stalled helper return = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("helper call did not return after close")
	}
}

func TestLifecycleRejectsConcurrentStartAndCloseReapsOwner(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	marker := filepath.Join(root, "owner-started")
	t.Setenv("NARUTO_TIMER_HELPER_TEST", marker)
	var lifecycle Lifecycle
	ownerResult := make(chan error, 1)
	go func() {
		_, err := RunWithLifecycle(context.Background(), &lifecycle, os.Args[0], "-test.run=TestCaptureHelperProcess", []byte("owner"))
		ownerResult <- err
	}()
	waitForFile(t, marker)

	_, err := RunWithLifecycle(context.Background(), &lifecycle, os.Args[0], "-test.run=TestCaptureHelperProcess", []byte("second"))
	if !errors.Is(err, ErrLifecycleActive) {
		t.Fatalf("second helper error = %v, want %v", err, ErrLifecycleActive)
	}

	closed := make(chan struct{})
	go func() { lifecycle.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel and reap lifecycle owner")
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "owner-started" {
		t.Fatalf("concurrent lifecycle left temporary artifacts: entries=%v err=%v", entries, readErr)
	}
	assertNoHelperTempDirs(t, entries)
	select {
	case err := <-ownerResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("owner helper error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("owner helper did not return after close")
	}
}

func TestRunRejectsResultFromNonZeroHelper(t *testing.T) {
	t.Setenv("NARUTO_TIMER_HELPER_TEST", "nonzero")
	if _, transaction, err := RunWithLifecycleTransaction(context.Background(), nil, os.Args[0], "-test.run=TestCaptureHelperProcess", []byte("nonzero")); err == nil {
		t.Fatal("accepted result written by a non-zero helper")
	} else if transaction.Outcome != OutcomeSDKWorker || transaction.Reap != ReapCompleted {
		t.Fatalf("nonzero helper transaction = %+v", transaction)
	} else if strings.Contains(err.Error(), "private-helper-output") {
		t.Fatal("helper stdout/stderr leaked through returned error")
	}
}

func TestRunClassifiesMissingResultAsHelperProtocol(t *testing.T) {
	t.Setenv("NARUTO_TIMER_HELPER_TEST", "missing-result")
	if _, transaction, err := RunWithLifecycleTransaction(context.Background(), nil, os.Args[0], "-test.run=TestCaptureHelperProcess", []byte("missing")); err == nil {
		t.Fatal("accepted helper without a result")
	} else if transaction.Outcome != OutcomeHelperProtocol || transaction.Reap != ReapCompleted {
		t.Fatalf("missing-result transaction = %+v", transaction)
	}
}

func assertNoHelperTempDirs(t *testing.T, entries []os.DirEntry) {
	t.Helper()
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "naruto-timer-capture-") {
			t.Fatalf("helper temporary directory survived close: %s", entry.Name())
		}
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("helper did not create %s", path)
}

// TestCaptureHelperProcess is re-executed by the tests above as a controllable
// helper protocol. A first invocation blocks until CommandContext kills/reaps
// it. The next invocation returns only its own request bytes.
func TestCaptureHelperProcess(t *testing.T) {
	marker := os.Getenv("NARUTO_TIMER_HELPER_TEST")
	if marker == "" {
		return
	}
	if len(os.Args) < 3 {
		os.Exit(2)
	}
	requestPath, resultPath := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	if marker == "slow" {
		time.Sleep(1201 * time.Millisecond)
		request, err := os.ReadFile(requestPath)
		if err != nil {
			os.Exit(3)
		}
		if err := os.WriteFile(resultPath, request, 0o600); err != nil {
			os.Exit(4)
		}
		return
	}
	if marker == "missing-result" {
		return
	}
	if marker == "nonzero" {
		fmt.Fprint(os.Stderr, `C:\Temp\private-helper-output\secret.dll token=private`)
		if err := os.WriteFile(resultPath, []byte("must-reject"), 0o600); err != nil {
			os.Exit(4)
		}
		os.Exit(9)
	}
	if f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
		f.Close()
		time.Sleep(time.Hour)
	}
	request, err := os.ReadFile(requestPath)
	if err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(resultPath, request, 0o600); err != nil {
		os.Exit(4)
	}
}
