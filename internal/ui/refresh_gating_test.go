package ui

import (
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"narutotimer/internal/config"
	"narutotimer/internal/diagnostics"
	"narutotimer/internal/ninja"
)

// uiCounter counts the overlay's posted closures and widget refreshes. With
// deferred set, posted closures queue until flush, like a busy Fyne thread.
type uiCounter struct {
	posts     int
	refreshes map[fyne.CanvasObject]int
	deferred  bool
	queue     []func()
}

func countUI(t *testing.T) *uiCounter {
	t.Helper()
	c := &uiCounter{refreshes: map[fyne.CanvasObject]int{}}
	post, refresh := postUI, refreshUI
	postUI = func(fn func()) {
		c.posts++
		if c.deferred {
			c.queue = append(c.queue, fn)
			return
		}
		post(fn)
	}
	refreshUI = func(o fyne.CanvasObject) {
		c.refreshes[o]++
		refresh(o)
	}
	t.Cleanup(func() { postUI, refreshUI = post, refresh })
	return c
}

func (c *uiCounter) flush() {
	for len(c.queue) > 0 {
		fn := c.queue[0]
		c.queue = c.queue[1:]
		fn()
	}
}

func (c *uiCounter) reset() {
	c.posts = 0
	c.refreshes = map[fyne.CanvasObject]int{}
}

func gatingSession(t *testing.T, mutate func(*config.Config)) *session {
	t.Helper()
	a := fynetest.NewApp()
	t.Cleanup(a.Quit)
	s := &session{cfg: config.Default(), side: "left"}
	if mutate != nil {
		mutate(&s.cfg)
	}
	s.win = fynetest.NewTempWindow(t, s.overlayContent())
	s.win.SetPadded(false)
	s.win.Resize(fyne.NewSize(280, 160))
	s.scene, s.fighting, s.beads = "fight", true, testBeads(2, 3)
	return s
}

const gatingFrames = 600

// 600 identical frames at 60 fps (10 s) with idle clocks: only overlay work
// that changes something should reach the Fyne thread.
func TestOverlayWorkOverIdenticalFrames(t *testing.T) {
	s := gatingSession(t, nil)
	c := countUI(t)
	for i := 0; i < gatingFrames; i++ {
		s.refreshOverlay()
	}
	t.Logf("idle overlay, %d identical frames: posts=%d tag=%d info=%d cd=%d alt=%d left=%d right=%d overlay=%d",
		gatingFrames, c.posts, c.refreshes[s.tag], c.refreshes[s.info], c.refreshes[s.cd], c.refreshes[s.altCD],
		c.refreshes[s.leftBothCD], c.refreshes[s.rightBothCD], c.refreshes[s.overlay])
}

// 600 frames of one live opponent clock at 60 fps: the clock text changes
// every tenth, but the hidden both-side and alternate widgets do not need
// a refresh for each of those changes.
func TestClockWorkOverLiveFrames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"single", nil},
		{"both", func(cfg *config.Config) { cfg.UI.ShowBothSides = true }},
		{"dual", func(cfg *config.Config) { cfg.UI.NinjaQuery = ninja.FifthMizukage }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := gatingSession(t, tc.mutate)
			at := time.Now()
			s.right.Observe(3, true, at.Add(-16*time.Millisecond), s.cooldown(), 2)
			s.right.Observe(2, true, at, s.cooldown(), 2)
			s.right.Observe(2, true, at.Add(16*time.Millisecond), s.cooldown(), 2)
			c := countUI(t)
			const step = time.Second / 60
			for i := 0; i < gatingFrames; i++ {
				s.refreshClockAt(at.Add(16*time.Millisecond + time.Duration(i)*step))
			}
			t.Logf("%s live clock, %d frames: posts=%d cd=%d alt=%d left=%d right=%d overlay=%d",
				tc.name, gatingFrames, c.posts, c.refreshes[s.cd], c.refreshes[s.altCD],
				c.refreshes[s.leftBothCD], c.refreshes[s.rightBothCD], c.refreshes[s.overlay])
		})
	}
}

func overlayPosts(s *session) uint64 { return atomic.LoadUint64(&s.overlayRevision) }

func TestIdenticalFramesPostOneOverlayUpdate(t *testing.T) {
	s := gatingSession(t, nil)
	c := countUI(t)
	for i := 0; i < gatingFrames; i++ {
		s.refreshOverlay()
	}
	// One overlay post plus the first clock post; no tag/info redraw after the first.
	if got := overlayPosts(s); got != 1 {
		t.Fatalf("overlay posts over %d identical frames = %d, want 1", gatingFrames, got)
	}
	if c.posts > 2 || c.refreshes[s.tag] > 1 || c.refreshes[s.info] > 1 {
		t.Fatalf("identical frames: posts=%d tag=%d info=%d", c.posts, c.refreshes[s.tag], c.refreshes[s.info])
	}
	if s.tag.Text != "对面·右 · 忍者未确认" || s.info.Text != "决斗场 · 对局  豆 左2/右3" {
		t.Fatalf("applied overlay: tag %q info %q", s.tag.Text, s.info.Text)
	}
}

func TestChangedOverlayValuesAndTracesAlwaysPost(t *testing.T) {
	s := gatingSession(t, nil)
	countUI(t)
	s.refreshOverlay()
	for _, tc := range []struct {
		name   string
		change func()
		check  func() bool
	}{
		{"label", func() { s.side = "right" }, func() bool { return s.tag.Text == "对面·左 · 忍者未确认" }},
		{"info", func() { s.beads = testBeads(1, 3) }, func() bool { return s.info.Text == "决斗场 · 对局  豆 左1/右3" }},
		{"status", func() { s.textStatus = "ready" }, func() bool { return true }},
		{"text off", func() { s.cfg.UI.AutoTextRecognition = false }, func() bool { return true }},
		// Only the diagnostics label shows the raw state while text is off.
		{"raw text status", func() { s.textStatus = "reading" }, func() bool { return true }},
		{"capture status", func() { s.status = "已保存 a.png" }, func() bool { return true }},
		{"trace", func() { s.traceFrame = traceRef{id: 7} }, func() bool { return s.traceOverlay == traceRef{id: 7} }},
		{"next trace", func() { s.traceFrame = traceRef{id: 8} }, func() bool { return s.traceOverlay == traceRef{id: 8} }},
		// Leaving diagnostics must still clear the applied trace.
		{"no trace", func() { s.traceFrame = traceRef{} }, func() bool { return s.traceOverlay == traceRef{} }},
	} {
		before := overlayPosts(s)
		s.mu.Lock()
		tc.change()
		s.mu.Unlock()
		s.refreshOverlay()
		if overlayPosts(s) != before+1 || !tc.check() {
			t.Fatalf("%s: posts %d -> %d, tag %q info %q trace %+v", tc.name, before, overlayPosts(s), s.tag.Text, s.info.Text, s.traceOverlay)
		}
		s.refreshOverlay()
		if overlayPosts(s) != before+1 {
			t.Fatalf("%s: repeated frame posted again", tc.name)
		}
	}
}

// A queued closure is superseded by any newer post, so the gate must compare
// with the newest posted values, never with what the screen showed earlier.
func TestSupersededOverlayPostNeverSuppressesUpdate(t *testing.T) {
	s := gatingSession(t, nil)
	c := countUI(t)
	s.refreshOverlay()
	c.deferred = true
	s.mu.Lock()
	s.side = "right"
	s.mu.Unlock()
	s.refreshOverlay() // queued: 对面·左
	s.mu.Lock()
	s.side = "left"
	s.mu.Unlock()
	s.refreshOverlay() // queued: back to the value still on screen
	s.refreshOverlay() // equal to the newest post: skipped
	if got := overlayPosts(s); got != 3 {
		t.Fatalf("overlay posts = %d, want 3", got)
	}
	c.flush()
	if s.tag.Text != "对面·右 · 忍者未确认" {
		t.Fatalf("superseded post left %q", s.tag.Text)
	}
	// The same holds for a trace that only a superseded closure carried.
	s.mu.Lock()
	s.traceFrame = traceRef{id: 41}
	s.mu.Unlock()
	s.refreshOverlay()
	s.mu.Lock()
	s.traceFrame = traceRef{id: 42}
	s.mu.Unlock()
	s.refreshOverlay()
	s.refreshOverlay()
	c.flush()
	if got := overlayPosts(s); got != 5 || s.traceOverlay != (traceRef{id: 42}) {
		t.Fatalf("trace posts = %d, applied %+v", got, s.traceOverlay)
	}
	// Rebuilt widgets start from defaults, so the next frame must post again.
	c.deferred = false
	s.win.SetContent(s.overlayContent())
	s.refreshOverlay()
	if got := overlayPosts(s); got != 6 || s.tag.Text != "对面·右 · 忍者未确认" {
		t.Fatalf("rebuilt overlay: posts %d tag %q", got, s.tag.Text)
	}
}

func TestHiddenClockLabelsShowCurrentTextWhenShown(t *testing.T) {
	s := gatingSession(t, func(cfg *config.Config) { cfg.UI.NinjaQuery = ninja.FifthMizukage })
	c := countUI(t)
	at := time.Now()
	s.right.Observe(3, true, at.Add(-16*time.Millisecond), s.cooldown(), 2)
	s.right.Observe(2, true, at, s.cooldown(), 2)
	s.right.Observe(2, true, at.Add(16*time.Millisecond), s.cooldown(), 2)
	setBoth := func(on bool) {
		s.mu.Lock()
		s.cfg.UI.ShowBothSides = on
		s.mu.Unlock()
		s.applyBothSideVisibility()
	}
	setDual := func(on bool) {
		s.mu.Lock()
		s.cfg.UI.NinjaQuery = ""
		if on {
			s.cfg.UI.NinjaQuery = ninja.FifthMizukage
		}
		s.mu.Unlock()
	}
	want := func(step string, elapsed time.Duration, right, alt string) {
		t.Helper()
		s.refreshClockAt(at.Add(elapsed))
		if s.rightBothCD.Text != right || s.leftBothCD.Text != "—" || s.altCD.Text != alt {
			t.Fatalf("%s: left %q right %q alt %q", step, s.leftBothCD.Text, s.rightBothCD.Text, s.altCD.Text)
		}
		if s.bothSideBox.Visible() && (s.leftBothDirty || s.rightBothDirty) {
			t.Fatalf("%s: visible both-side labels left undrawn", step)
		}
		if s.alternateBox.Visible() && s.altDirty {
			t.Fatalf("%s: visible alternate label left undrawn", step)
		}
	}
	want("dual", time.Second, "14.0", "9.0")
	if c.refreshes[s.rightBothCD] != 0 || c.refreshes[s.altCD] == 0 {
		t.Fatalf("hidden both-side refreshed %d, visible alt %d", c.refreshes[s.rightBothCD], c.refreshes[s.altCD])
	}
	setDual(false)
	want("single", 2*time.Second, "13.0", "")
	altRefreshes := c.refreshes[s.altCD]
	want("single later", 3*time.Second, "12.0", "")
	if c.refreshes[s.altCD] != altRefreshes || c.refreshes[s.rightBothCD] != 0 {
		t.Fatalf("hidden labels refreshed: alt %d right %d", c.refreshes[s.altCD]-altRefreshes, c.refreshes[s.rightBothCD])
	}
	setBoth(true)
	want("both shown", 4*time.Second, "11.0", "")
	setBoth(false)
	want("both hidden", 5*time.Second, "10.0", "")
	setDual(true)
	setBoth(true)
	want("both and dual", 6*time.Second, "9.0", "4.0")
	want("same tenth", 6*time.Second, "9.0", "4.0")
	setDual(false)
	setBoth(false)
	want("final hidden", 11*time.Second, "4.0", "")
	setDual(true)
	setBoth(true)
	want("final shown", 11*time.Second, "4.0", "就绪")
}

// While a recorder exists its counters reach the diagnostics label only
// through the overlay closure, so every frame still posts.
func TestDiagnosticsKeepPerFrameOverlayPosts(t *testing.T) {
	s := gatingSession(t, nil)
	countUI(t)
	s.diagnosticMu.Lock()
	s.diagnosticLast = &diagnostics.Recorder{}
	s.diagnosticMu.Unlock()
	for i := 0; i < 5; i++ {
		s.refreshOverlay()
	}
	if got := overlayPosts(s); got != 5 {
		t.Fatalf("overlay posts with diagnostics = %d, want 5", got)
	}
}
