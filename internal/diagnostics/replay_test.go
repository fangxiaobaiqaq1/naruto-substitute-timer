package diagnostics

import (
	"archive/zip"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"narutotimer/internal/frame"
)

func replayTestFrame(at time.Time, c color.RGBA) frame.Frame {
	f := validFrame(at)
	f.Img = image.NewRGBA(image.Rect(0, 0, 1200, 20))
	for i := 0; i < len(f.Img.Pix); i += 4 {
		f.Img.Pix[i], f.Img.Pix[i+1], f.Img.Pix[i+2], f.Img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	return f
}

func readReplayManifest(t *testing.T, r *Recorder) replayManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.Snapshot().Directory, "replay", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got replayManifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReplayDisabledByDefault(t *testing.T) {
	r, err := New(Options{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if r.replay != nil {
		t.Fatal("replay enabled without explicit run-local option")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Snapshot().Directory, "replay")); !os.IsNotExist(err) {
		t.Fatalf("disabled replay created spool: %v", err)
	}
}

func TestReplayPartialHistoryAndDeepCopy(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), Replay: true, ReplayDuration: 3 * time.Second, ReplayInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-2 * time.Second)
	f := replayTestFrame(base, color.RGBA{R: 7})
	r.Observe(f, base)
	for i := 0; i < len(f.Img.Pix); i += 4 {
		f.Img.Pix[i] = 99
	} // source can be reused immediately
	r.Observe(replayTestFrame(base.Add(time.Second), color.RGBA{R: 8}), base.Add(time.Second))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	m := readReplayManifest(t, r)
	if !m.PartialHistory || m.Saved != 2 || len(m.Frames) != 2 {
		t.Fatalf("manifest=%+v", m)
	}
	if m.Frames[0].OutputWidth != replayMaxWidth || m.Frames[0].State != "saved" || m.Frames[0].RawPath == "" || m.Frames[0].AnnotatedPath == "" {
		t.Fatalf("frame=%+v", m.Frames[0])
	}
	if _, err := os.Stat(filepath.Join(r.Snapshot().Directory, filepath.FromSlash(m.Frames[0].AnnotatedPath))); err != nil {
		t.Fatal(err)
	}
	if m.Frames[0].AnnotatedHeight <= m.Frames[0].OutputHeight {
		t.Fatalf("annotation did not add footer: %+v", m.Frames[0])
	}
	imagePath := filepath.Join(r.Snapshot().Directory, filepath.FromSlash(m.Frames[0].RawPath))
	file, err := os.Open(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	got := color.RGBAModel.Convert(decoded.At(0, 0)).(color.RGBA).R
	if got == 99 {
		t.Fatalf("replay retained caller buffer instead of deep copy: %d", got)
	}
	annotatedFile, err := os.Open(filepath.Join(r.Snapshot().Directory, filepath.FromSlash(m.Frames[0].AnnotatedPath)))
	if err != nil {
		t.Fatal(err)
	}
	annotated, err := jpeg.Decode(annotatedFile)
	_ = annotatedFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if annotated.Bounds().Dx() != decoded.Bounds().Dx() || annotated.Bounds().Dy() <= decoded.Bounds().Dy() {
		t.Fatalf("annotated dimensions=%v raw=%v", annotated.Bounds(), decoded.Bounds())
	}
	if color.RGBAModel.Convert(annotated.At(2, decoded.Bounds().Dy()+2)) == color.RGBAModel.Convert(decoded.At(2, 2)) {
		t.Fatal("annotated footer did not differ from raw")
	}
}

func TestReplayUsesSmallDefaultSamplingPolicy(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), Replay: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.opts.ReplayInterval != time.Second || r.opts.MaxReplayBytes != 64<<20 {
		t.Fatalf("defaults=%+v", r.opts)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReplayDimensionsMetadataAndExportStayBounded(t *testing.T) {
	for _, size := range []image.Point{{1280, 720}, {1920, 1080}} {
		t.Run(size.String(), func(t *testing.T) {
			r, err := New(Options{Root: t.TempDir(), Replay: true})
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now()
			f := validFrame(at)
			f.Img = image.NewRGBA(image.Rectangle{Max: size})
			f.Scene, f.Fighting, f.LeftNinja, f.RightNinja, f.LeftSlots, f.RightSlots = "lobby", false, "左忍者", "右忍者", 4, 4
			f.Hold, f.Duplicate, f.Status = true, true, "识别不确定"
			f.AnalysisStarted, f.AnalyzedAt = at.Add(2*time.Millisecond), at.Add(3*time.Millisecond)
			r.Observe(f, at.Add(4*time.Millisecond))
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			m := readReplayManifest(t, r)
			if m.MaxWidth != 720 || m.Quality != 75 || m.ByteCap != 64<<20 || m.Saved != 1 {
				t.Fatalf("manifest=%+v", m)
			}
			entry := m.Frames[0]
			if entry.OutputWidth != 720 || entry.OutputHeight != size.Y*720/size.X || entry.StateEvidence.Scene != "lobby" || entry.StateEvidence.LeftNinja != "左忍者" || !entry.StateEvidence.Hold || !entry.StateEvidence.Duplicate || entry.OffsetBeforeStopMS == nil {
				t.Fatalf("frame evidence/size missing: %+v", entry)
			}
			if r.Snapshot().ReplayBytes > 64<<20 {
				t.Fatalf("replay cap exceeded: %+v", r.Snapshot())
			}
			out, err := r.ExportReplay("")
			if err != nil {
				t.Fatal(err)
			}
			z, err := zip.OpenReader(out)
			if err != nil {
				t.Fatal(err)
			}
			defer z.Close()
			if len(z.File) != 3 || z.File[0].Name != "manifest.json" { // manifest + raw + annotated
				t.Fatalf("replay export contains unexpected payload: %+v", z.File)
			}
		})
	}
}

func TestReplayAcceptsValidNonFightAndHeldImages(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), Replay: true, ReplayInterval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Second)
	lobby := replayTestFrame(at, color.RGBA{R: 1})
	lobby.Fighting, lobby.Scene = false, "lobby"
	held := replayTestFrame(at.Add(time.Millisecond), color.RGBA{R: 2})
	held.Hold = true
	r.Observe(lobby, at)
	r.Observe(held, at.Add(time.Millisecond))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	m := readReplayManifest(t, r)
	if m.Saved != 2 || len(m.Frames) != 2 {
		t.Fatalf("non-fight/held diagnostic images missing: %+v", m)
	}
}

func TestReplayRetentionAndCapEviction(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), Replay: true, ReplayDuration: time.Second, ReplayInterval: time.Millisecond, MaxReplayBytes: 4000})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-3 * time.Second)
	for i := 0; i < 4; i++ {
		r.Observe(replayTestFrame(base.Add(time.Duration(i)*time.Second), color.RGBA{R: byte(i)}), base)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	m := readReplayManifest(t, r)
	if m.Evicted == 0 || m.Saved == 0 {
		t.Fatalf("expected cap/window eviction: %+v", m)
	}
	if got := r.Snapshot().ReplaySaved; got != m.Saved {
		t.Fatalf("saved counter includes evicted frames: status=%d manifest=%d", got, m.Saved)
	}
	for _, f := range m.Frames {
		if f.State == "evicted" {
			for _, rel := range []string{f.RawPath, f.AnnotatedPath} {
				if rel == "" {
					continue
				}
				if _, err := os.Stat(filepath.Join(r.Snapshot().Directory, filepath.FromSlash(rel))); !os.IsNotExist(err) {
					t.Fatalf("evicted remains: %v", err)
				}
			}
		}
	}
}

func TestReplayQueueFullDoesNotBlockObserve(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), Replay: true, ReplayInterval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	// Isolate a full queue without a worker: this is the exact admission helper
	// called under Recorder.mu, and proves it never waits for JPEG/disk work.
	q := &replayRecorder{parent: r, interval: time.Nanosecond, queue: make(chan replayWork, 1)}
	q.queue <- replayWork{}
	start := time.Now()
	r.mu.Lock()
	q.enqueueLocked(replayWork{img: replayTestFrame(start, color.RGBA{R: 3}).Img, capturedAt: start})
	r.mu.Unlock()
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("replay admission blocked on full queue: %s", elapsed)
	}
	if r.Snapshot().ReplayDropped != 1 {
		t.Fatalf("full replay queue was not reported: %+v", r.Snapshot())
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceFileSupportsRepeatReportExport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp, err := os.CreateTemp(dir, ".report-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.WriteString("new"); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(tmp.Name(), path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new" {
		t.Fatalf("replacement failed: %q, %v", got, err)
	}
}

func TestReplayManifestNeverListsPartialFiles(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), Replay: true})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	r.Observe(replayTestFrame(at, color.RGBA{R: 4}), at)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	m := readReplayManifest(t, r)
	for _, entry := range m.Frames {
		if entry.State == "partial" || filepath.Ext(entry.Path) == ".partial" || filepath.Ext(entry.RawPath) == ".partial" || filepath.Ext(entry.AnnotatedPath) == ".partial" {
			t.Fatalf("manifest listed incomplete replay output: %+v", entry)
		}
	}
	partials, err := filepath.Glob(filepath.Join(r.Snapshot().Directory, "replay", "**", "*.partial"))
	if err != nil || len(partials) != 0 {
		t.Fatalf("partial replay files remain: %v, %v", partials, err)
	}
}

func TestAnnotatedReplayUsesSameFrameUIState(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), Replay: true})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1700000000, 0)
	id := r.Observe(replayTestFrame(at, color.RGBA{R: 31}), at)
	r.RecordReplayUIState(ReplayUIState{FrameID: id, PlayerSide: "left", OpponentSide: "right", OpponentNinja: "Right Ninja", PrimaryText: "14.9", EventText: "event 7", LeftEventCount: 1, RightEventCount: 7, PreparedAt: at.Add(time.Millisecond)})
	r.RecordReplayUIApplied(id, "14.5", "", "event 7")
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	m := readReplayManifest(t, r)
	entry := m.Frames[0]
	if entry.UIState.FrameID != id || entry.UIState.PrimaryText != "14.9" || entry.UIState.AppliedPrimaryText != "14.5" || entry.UIState.EventText != "event 7" || entry.UIState.Unavailable != "" {
		t.Fatalf("wrong same-frame UI state: %+v", entry.UIState)
	}
	text := strings.Join(replayLines(entry), "\n")
	for _, want := range []string{"frame_id=1", "timer prepared=14.9", "timer applied=14.5", "event=event 7", "Right Ninja"} {
		if !strings.Contains(text, want) {
			t.Fatalf("annotation lacks %q: %s", want, text)
		}
	}
}

func TestAnnotatedReplayExplicitlyMarksUnavailableTimer(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), Replay: true})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	r.Observe(replayTestFrame(at, color.RGBA{R: 32}), at)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	entry := readReplayManifest(t, r).Frames[0]
	if entry.UIState.Unavailable != "not available on this frame" {
		t.Fatalf("fabricated UI state: %+v", entry.UIState)
	}
	if !strings.Contains(strings.Join(replayLines(entry), "\n"), "not available on this frame") {
		t.Fatal("annotation omitted unavailable marker")
	}
}

func TestReplayManifestCorrelatesTimerEvents(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), Replay: true, ReplayInterval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Second)
	first := r.Observe(replayTestFrame(at, color.RGBA{R: 1}), at)
	confirmedAt := at.Add(time.Millisecond)
	confirmed := r.Observe(replayTestFrame(confirmedAt, color.RGBA{R: 2}), confirmedAt)
	r.Confirm(Event{FrameID: confirmed, Side: "right", Serial: 9, Number: 3,
		FirstObservedAt: at.Add(time.Millisecond), ConfirmedAt: confirmedAt.Add(4 * time.Millisecond), UIVisible: true})
	carrierAt := at.Add(2 * time.Millisecond)
	carrier := r.Observe(replayTestFrame(carrierAt, color.RGBA{R: 3}), carrierAt)
	r.CarryEvents(carrier, 0, 9)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	m := readReplayManifest(t, r)
	var confirmation, display *replayTimerEvent
	for _, entry := range m.Frames {
		for i := range entry.TimerEvents {
			e := &entry.TimerEvents[i]
			if entry.FrameID == confirmed && e.ConfirmationFrameID == confirmed {
				confirmation = e
			}
			if entry.FrameID == carrier && e.DisplayFrameID == carrier {
				display = e
			}
		}
	}
	if confirmation == nil || display == nil || confirmation.FirstFrameID != first || confirmation.Serial != 9 || confirmation.Number != 3 {
		t.Fatalf("timer/frame correlation missing: confirmation=%+v display=%+v manifest=%+v", confirmation, display, m)
	}
}

func TestReplayStopRejectsAfterCutoffAndDoesNotBlock(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), Replay: true, ReplayInterval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	r.Observe(replayTestFrame(at, color.RGBA{R: 1}), at)
	done := make(chan error, 1)
	go func() { done <- r.Close() }()
	deadline := time.Now().Add(time.Second)
	for !r.Snapshot().Closed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !r.Snapshot().Closed {
		t.Fatal("close did not establish a replay cutoff")
	}
	postCutoff := time.Now()
	r.Observe(replayTestFrame(postCutoff, color.RGBA{R: 2}), postCutoff)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	m := readReplayManifest(t, r)
	if m.Saved != 1 || m.Frames[0].CapturedAt.After(m.Cutoff) {
		t.Fatalf("post-cutoff entered replay: %+v", m)
	}
}
