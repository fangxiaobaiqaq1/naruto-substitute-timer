package diagnostics

import (
	"bytes"
	"image"
	"math/rand"
	"testing"
	"time"
)

// GE-08: the UI reports a snapshot for every frame; only frames admitted to
// the replay (≤ 1 per ReplayInterval, inside the window) may keep one. The
// auditor measured 200 000 entries (+44.6 MiB) for a 55-minute session.
func TestReplayUIStateBoundedByReplayWindow(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), Replay: true, MaxObservations: 100000})
	if err != nil {
		t.Fatal(err)
	}
	const frames = 24000 // 384 s of 60 Hz capture, more than twice the 180 s window
	start := time.Now().Add(-frames * 16 * time.Millisecond)
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for i := 0; i < frames; i++ {
		f := validFrame(start.Add(time.Duration(i) * 16 * time.Millisecond))
		f.Img = img
		id := r.Observe(f, f.AnalyzedAt.Add(time.Millisecond))
		r.RecordReplayUIState(ReplayUIState{FrameID: id, PlayerSide: "left", PrimaryText: "14.9", PreparedAt: time.Now()})
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r.replay.mu.Lock()
	ui, admitted := len(r.replay.ui), len(r.replay.admitted)
	r.replay.mu.Unlock()
	limit := int(defaultReplayDuration/defaultReplayInterval) + defaultReplayQueue + 2
	if ui > limit || admitted > limit {
		t.Fatalf("replay kept %d UI states / %d admitted frames for %d frames; want ≤ %d", ui, admitted, frames, limit)
	}
	m := readReplayManifest(t, r)
	withState := 0
	for _, entry := range m.Frames {
		if entry.State == "saved" {
			if entry.UIState.FrameID != entry.FrameID || entry.UIState.PrimaryText != "14.9" {
				t.Fatalf("saved frame lost its same-frame UI state: %+v", entry.UIState)
			}
			withState++
		}
	}
	if withState == 0 {
		t.Fatal("no saved replay frame")
	}
}

func randomRGBA(rng *rand.Rand, r image.Rectangle) *image.RGBA {
	img := image.NewRGBA(r)
	rng.Read(img.Pix)
	return img
}

func copyOf(src *image.RGBA) *image.RGBA {
	out := image.NewRGBA(src.Bounds())
	for y := src.Rect.Min.Y; y < src.Rect.Max.Y; y++ {
		copy(out.Pix[out.PixOffset(src.Rect.Min.X, y):out.PixOffset(src.Rect.Max.X, y)], src.Pix[src.PixOffset(src.Rect.Min.X, y):src.PixOffset(src.Rect.Max.X, y)])
	}
	return out
}

// GE-14: sampling while copying gives exactly the pixels of the former
// "deep copy, then scaleReplay", and never aliases the capture buffer.
func TestSampleReplayMatchesFullCopyThenScale(t *testing.T) {
	rng := rand.New(rand.NewSource(14))
	full := randomRGBA(rng, image.Rect(0, 0, 3840, 2160))
	cases := []*image.RGBA{
		randomRGBA(rng, image.Rect(0, 0, 1920, 1080)),
		randomRGBA(rng, image.Rect(0, 0, 721, 5)),
		randomRGBA(rng, image.Rect(0, 0, 720, 400)),
		randomRGBA(rng, image.Rect(0, 0, 33, 7)),
		full.SubImage(image.Rect(100, 50, 2660, 1490)).(*image.RGBA),
	}
	for _, src := range cases {
		want := scaleReplay(copyOf(src))
		got := sampleReplay(src)
		if got.Rect != want.Rect || !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("%v: sampled replay differs from copy+scale", src.Rect)
		}
		before := append([]byte(nil), got.Pix...)
		for i := range src.Pix {
			src.Pix[i] ^= 0xff
		}
		if !bytes.Equal(got.Pix, before) {
			t.Fatalf("%v: sample aliases the capture buffer", src.Rect)
		}
	}
}

// GE-14: the queued replay sample is at most replayMaxWidth wide (it used to
// be a full-resolution copy: up to 4×132 MiB for an 8K PrintWindow client).
func TestReplayQueueHoldsDownscaledSamples(t *testing.T) {
	r, err := New(Options{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	q := &replayRecorder{parent: r, interval: time.Nanosecond, queue: make(chan replayWork, 1)}
	src := image.NewRGBA(image.Rect(0, 0, 3840, 2160))
	r.mu.Lock()
	q.enqueueLocked(replayWork{img: src, frameID: 7, capturedAt: time.Now(), sourceWidth: 3840, sourceHeight: 2160})
	r.mu.Unlock()
	w := <-q.queue
	if w.img.Bounds().Dx() > replayMaxWidth || w.img == src {
		t.Fatalf("queued replay sample is %v", w.img.Bounds())
	}
	if w.sourceWidth != 3840 || w.sourceHeight != 2160 {
		t.Fatalf("source size lost: %dx%d", w.sourceWidth, w.sourceHeight)
	}
	if _, ok := q.admitted[7]; !ok {
		t.Fatal("queued frame not admitted for its UI state")
	}
}
