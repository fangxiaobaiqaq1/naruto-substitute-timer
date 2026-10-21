package ui

import (
	"testing"
	"time"
)

// GE-13: Run waits for diagnosticWriters before returning. The replay export
// goroutine was not part of it, so quitting during an export left a partial
// .replay-*.zip in debug/sessions/replay-exports.
func TestReplayExportIsJoinedBeforeExit(t *testing.T) {
	s := &session{done: make(chan struct{})}
	release, finished, waited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s.exportReplayAsync(func() (string, error) {
		<-release
		return "replay.zip", nil
	}, func(string, error) { close(finished) })
	go func() {
		s.diagnosticWriters.Wait() // what Run does on exit
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("exit did not wait for the running replay export")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-waited
	select {
	case <-finished:
	default:
		t.Fatal("exit returned before the export finished")
	}
}
