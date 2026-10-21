// Package safego contains panics on the capture and analysis path.
//
// A panic on a goroutine that nobody recovers ends the whole process. In the
// windowed build (-H windowsgui) stderr goes nowhere, so the timer just
// disappeared. Group carries a helper goroutine's panic back to the goroutine
// that waits for it, where the analysis entry point turns it into an error for
// that frame; Report writes the stack to the crash log (SetLog).
package safego

import (
	"fmt"
	"io"
	"runtime/debug"
	"sync"
	"time"
)

// PanicError is a recovered panic with the stack of the goroutine that panicked.
type PanicError struct {
	Value any
	Stack []byte
}

func (p *PanicError) Error() string { return fmt.Sprint("panic: ", p.Value) }

// Recovered wraps a value returned by recover (nil for nil). Call it in the
// deferred function itself so the stack still shows where the panic happened;
// a PanicError that Group.Wait re-raised keeps its helper goroutine's stack.
func Recovered(value any) *PanicError {
	if value == nil {
		return nil
	}
	if p, ok := value.(*PanicError); ok {
		return p
	}
	return &PanicError{Value: value, Stack: debug.Stack()}
}

// Group is a sync.WaitGroup for fan-out work whose helper panics reach Wait's
// caller instead of ending the process.
type Group struct {
	wg    sync.WaitGroup
	mu    sync.Mutex
	first *PanicError
}

// Go runs fn on a new goroutine. A panic in fn is recovered there and kept.
func (g *Group) Go(fn func()) {
	g.wg.Go(func() {
		defer func() { g.keep(Recovered(recover())) }()
		fn()
	})
}

// Run does the caller's share of the work on the calling goroutine. A panic in
// fn is kept like a helper's, so Wait still joins every helper before
// re-raising it: no helper keeps running on state its caller has abandoned.
func (g *Group) Run(fn func()) {
	defer func() { g.keep(Recovered(recover())) }()
	fn()
}

func (g *Group) keep(p *PanicError) {
	if p == nil {
		return
	}
	g.mu.Lock()
	if g.first == nil {
		g.first = p
	}
	g.mu.Unlock()
}

// Wait waits for every goroutine started by Go, then re-panics on the calling
// goroutine with the first helper panic, if any.
func (g *Group) Wait() {
	g.wg.Wait()
	g.mu.Lock()
	p := g.first
	g.mu.Unlock()
	if p != nil {
		panic(p)
	}
}

// reportEvery limits how often Report writes a stack: a panic on every frame
// must not fill the disk. The first one is always written.
const reportEvery = time.Minute

var reports struct {
	mu         sync.Mutex
	w          io.Writer
	last       time.Time
	suppressed int
	now        func() time.Time
}

// SetLog sets where Report writes recovered panics; nil discards them.
func SetLog(w io.Writer) {
	reports.mu.Lock()
	reports.w = w
	reports.last, reports.suppressed = time.Time{}, 0
	reports.mu.Unlock()
}

// Report records a recovered panic with its stack: the first one at once, then
// at most one per minute with the number left out in between.
func Report(where string, p *PanicError) {
	reports.mu.Lock()
	defer reports.mu.Unlock()
	if reports.w == nil {
		return
	}
	now := time.Now()
	if reports.now != nil {
		now = reports.now()
	}
	if !reports.last.IsZero() && now.Sub(reports.last) < reportEvery {
		reports.suppressed++
		return
	}
	skipped := ""
	if reports.suppressed > 0 {
		skipped = fmt.Sprintf(" (%d more since the last report)", reports.suppressed)
	}
	fmt.Fprintf(reports.w, "%s recovered %s in %s%s\n%s\n", now.Format(time.RFC3339), p.Error(), where, skipped, p.Stack)
	reports.last, reports.suppressed = now, 0
}
