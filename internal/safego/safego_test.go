package safego

import (
	"bytes"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGroupReraisesHelperPanicOnWaitingGoroutine(t *testing.T) {
	var finished atomic.Int32
	recovered := func() (p *PanicError) {
		defer func() { p = Recovered(recover()) }()
		var g Group
		g.Go(func() { panic("helper failed") })
		g.Go(func() { finished.Add(1) })
		finished.Add(1) // the caller's own share of the work
		g.Wait()
		return nil
	}()
	if recovered == nil || recovered.Value != "helper failed" {
		t.Fatalf("recovered %+v", recovered)
	}
	if finished.Load() != 2 {
		t.Fatal("Wait returned before the other goroutines finished")
	}
	if !strings.Contains(string(recovered.Stack), "safego_test.go") {
		t.Fatalf("stack does not show the helper's panic site:\n%s", recovered.Stack)
	}
}

// A panic in the caller's own share must not leave helpers running on state
// the caller abandons (the next frame would race with them).
func TestGroupRunJoinsHelpersBeforeReraisingCallerPanic(t *testing.T) {
	release := make(chan struct{})
	var helperDone atomic.Bool
	p := func() (p *PanicError) {
		defer func() { p = Recovered(recover()) }()
		var g Group
		g.Go(func() {
			<-release
			helperDone.Store(true)
		})
		g.Run(func() {
			close(release)
			panic("caller share failed")
		})
		g.Wait()
		return nil
	}()
	if p == nil || p.Value != "caller share failed" || !helperDone.Load() {
		t.Fatalf("recovered %v, helper done %v", p, helperDone.Load())
	}
}

func TestGroupWithoutPanicIsAWaitGroup(t *testing.T) {
	var g Group
	var sum atomic.Int64
	for i := 1; i <= 100; i++ {
		g.Go(func() { sum.Add(int64(i)) })
	}
	g.Wait()
	if sum.Load() != 5050 {
		t.Fatalf("sum = %d", sum.Load())
	}
}

func TestRecoveredKeepsRuntimeErrors(t *testing.T) {
	p := func() (p *PanicError) {
		defer func() { p = Recovered(recover()) }()
		var s []int
		_ = s[3]
		return nil
	}()
	var runtimeErr interface{ RuntimeError() }
	if err, ok := p.Value.(error); !ok || !errors.As(err, &runtimeErr) || !strings.Contains(p.Error(), "index out of range") {
		t.Fatalf("got %v", p)
	}
}

func TestReportIsRateLimited(t *testing.T) {
	var log bytes.Buffer
	now := time.Unix(1_700_000_000, 0)
	SetLog(&log)
	reports.now = func() time.Time { return now }
	t.Cleanup(func() { SetLog(nil); reports.now = nil })
	p := &PanicError{Value: "boom", Stack: []byte("goroutine 1 [running]:")}
	Report("analysis", p)
	for i := 0; i < 5; i++ {
		now = now.Add(time.Second)
		Report("analysis", p)
	}
	now = now.Add(time.Minute)
	Report("analysis", p)
	out := log.String()
	if strings.Count(out, "recovered panic: boom in analysis") != 2 || !strings.Contains(out, "(5 more since the last report)") || !strings.Contains(out, "goroutine 1 [running]:") {
		t.Fatalf("log:\n%s", out)
	}
}
