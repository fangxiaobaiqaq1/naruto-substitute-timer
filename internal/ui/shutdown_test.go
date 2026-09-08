package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/canvas"
	"narutotimer/internal/config"
	"narutotimer/internal/frame"
)

func TestShutdownDropsInFlightCapture(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s := &session{cfg: config.Default(), done: make(chan struct{}), status: "before", side: "left"}
	s.provider = func() frame.Frame {
		close(started)
		<-release
		return frame.Frame{Status: "late frame", Scene: "fight", Fighting: true, PlayerSide: "right", Beads: testBeads(4, 4), CapturedAt: time.Now()}
	}
	returned := make(chan struct{})
	go func() { s.captureOnce(); close(returned) }()
	<-started
	close(s.done)
	close(release)
	<-returned
	if s.status != "before" || s.side != "left" || len(s.beads) != 0 || s.busy {
		t.Fatalf("late capture changed the closed session: status=%s side=%s beads=%d busy=%v", s.status, s.side, len(s.beads), s.busy)
	}
}

func TestShutdownSkipsCaptureAndUIRefresh(t *testing.T) {
	s := &session{cfg: config.Default(), done: make(chan struct{}), cd: canvas.NewText("before", clockIdle), tag: canvas.NewText("before", tagIdle)}
	close(s.done)
	s.provider = func() frame.Frame { t.Fatal("closed session started capture"); return frame.Frame{} }
	s.captureOnce()
	// There is intentionally no Fyne app: a closed session must not enqueue work.
	s.refreshClock()
	s.refreshOverlay()
	if s.clockRevision != 0 || s.overlayRevision != 0 {
		t.Fatal("closed session prepared a UI update")
	}
}

func TestSessionStopIsIdempotent(t *testing.T) {
	s := &session{done: make(chan struct{})}
	s.stop()
	s.stop() // window close plus Run's deferred cleanup
	if !s.stopped() || s.wait(time.Hour) {
		t.Fatal("stop did not cancel background waits")
	}
}
