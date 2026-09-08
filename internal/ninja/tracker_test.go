package ninja

import (
	"image"
	"image/draw"
	"os"
	"sync"
	"testing"
	"time"
)

func trackerImage(t testing.TB) *image.RGBA {
	t.Helper()
	f, err := os.Open("../../inbox/regressions/special-ninjas-20260907/obito-two.png")
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	return img
}

func TestTrackedNameRequiresCurrentPixels(t *testing.T) {
	r := NewReader()
	var tracker Tracker
	img := trackerImage(t)
	roi := NameRegion(image.Pt(87, 56), 1, true)
	at := time.Now()
	if got := tracker.Read(r, img, roi, 1, at); got.Name != Obito {
		t.Fatalf("first detection: %+v", got)
	}
	if got := tracker.Read(r, img, roi, 1, at.Add(time.Millisecond)); got.Name != Obito {
		t.Fatalf("fast verification: %+v", got)
	}
	draw.Draw(img, roi, image.Black, image.Point{}, draw.Src)
	if got := tracker.Read(r, img, roi, 1, at.Add(2*time.Millisecond)); got.Name != "" {
		t.Fatalf("stale name survived missing pixels: %+v", got)
	}
}

func TestTrackedNameRecoversNextFrameAfterBriefOcclusion(t *testing.T) {
	r := NewReader()
	var tracker Tracker
	img := trackerImage(t)
	roi := NameRegion(image.Pt(87, 56), 1, true)
	blank := image.NewRGBA(img.Bounds())
	at := time.Unix(1700000000, 0)
	if got := tracker.Read(r, img, roi, 1, at); got.Name != Obito {
		t.Fatalf("initial name: %+v", got)
	}
	searchDeadline := tracker.retryAfter
	if got := tracker.Read(r, blank, roi, 1, at.Add(16*time.Millisecond)); got.Name != "" {
		t.Fatalf("occluded frame reused a name: %+v", got)
	}
	if got := tracker.Read(r, img, roi, 1, at.Add(32*time.Millisecond)); got.Name != Obito {
		t.Fatalf("visible name waited for full-search backoff: %+v", got)
	}
	// Repeated glints may alternate visibility; every accepted frame must still
	// recheck current pixels rather than smoothing an old identity into the gap.
	for i := 3; i < 20; i++ {
		source, want := img, Obito
		if i%2 == 1 {
			source, want = blank, ""
		}
		if got := tracker.Read(r, source, roi, 1, at.Add(time.Duration(i)*16*time.Millisecond)); got.Name != want {
			t.Fatalf("frame %d: %q want %q", i, got.Name, want)
		}
	}
	if tracker.retryAfter != searchDeadline {
		t.Fatal("alternating occlusion bypassed the full-search rate limit")
	}
}

func TestUnverifiedNameRetainsOnlyBoundedTopology(t *testing.T) {
	r := NewReader()
	var tracker Tracker
	img := trackerImage(t)
	blank := image.NewRGBA(img.Bounds())
	roi := NameRegion(image.Pt(87, 56), 1, true)
	at := time.Unix(1700000000, 0)
	tracker.Read(r, img, roi, 1, at)
	got := tracker.Read(r, blank, roi, 1, at.Add(16*time.Millisecond))
	if !got.Unverified || got.Slots != 4 || got.Name != "" || got.Palette != "" || got.Score != 0 {
		t.Fatalf("missing label reused identity or dropped topology: %+v", got)
	}
	got = tracker.Read(r, blank, roi, 1, at.Add(1100*time.Millisecond))
	if got != (Readout{}) || tracker.hint.Name != "" {
		t.Fatalf("topology hint outlived expiry: %+v", got)
	}
	tracker.Read(r, img, roi, 1, at.Add(1700*time.Millisecond))
	got = tracker.Read(r, blank, roi.Add(image.Pt(0, 1)), 1, at.Add(1716*time.Millisecond))
	if got != (Readout{}) {
		t.Fatalf("geometry change inherited old topology: %+v", got)
	}
}

func BenchmarkTrackedSpecialName(b *testing.B) {
	img := trackerImage(b)
	roi := NameRegion(image.Pt(87, 56), 1, true)
	r := NewReader()
	var tracker Tracker
	tracker.Read(r, img, roi, 1, time.Now())
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		tracker.Read(r, img, roi, 1, time.Now())
	}
}

func TestUnknownRetryAndReplaySeekUseFrameTime(t *testing.T) {
	r := NewReader()
	var tracker Tracker
	img := trackerImage(t)
	blank := image.NewRGBA(img.Bounds())
	roi := NameRegion(image.Pt(87, 56), 1, true)
	at := time.Unix(1700000000, 0)
	if got := tracker.Read(r, blank, roi, 1, at); got.Name != "" {
		t.Fatal("blank")
	}
	if got := tracker.Read(r, img, roi, 1, at.Add(600*time.Millisecond)); got.Name != Obito {
		t.Fatalf("frame time did not expire unknown retry: %+v", got)
	}
	tracker.Read(r, blank, roi, 1, at.Add(time.Second))
	if got := tracker.Read(r, img, roi, 1, at); got.Name != Obito {
		t.Fatalf("replay seek retained future retry deadline: %+v", got)
	}
}

func TestReaderAndTrackerCanBeShared(t *testing.T) {
	img := trackerImage(t)
	r := NewReader()
	var tracker Tracker
	roi := NameRegion(image.Pt(87, 56), 1, true)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 5 {
				if got := r.Read(img, roi, 1); got.Name != Obito {
					t.Errorf("reader: %+v", got)
				}
				if got := tracker.Read(r, img, roi, 1, time.Now()); got.Name != Obito {
					t.Errorf("tracker: %+v", got)
				}
			}
		})
	}
	wg.Wait()
}
