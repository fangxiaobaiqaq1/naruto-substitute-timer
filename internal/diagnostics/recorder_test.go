package diagnostics

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"narutotimer/internal/frame"
)

func testRecorder(t *testing.T, record bool) *Recorder {
	t.Helper()
	r, err := New(Options{Root: t.TempDir(), RecordFrames: record})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

func validFrame(at time.Time) frame.Frame {
	return frame.Frame{Img: image.NewRGBA(image.Rect(0, 0, 4, 3)),
		CaptureStarted: at, CapturedAt: at.Add(time.Millisecond),
		AnalysisStarted: at.Add(2 * time.Millisecond), AnalyzedAt: at.Add(3 * time.Millisecond),
		CaptureMethod: "test-native", Scene: "fight", Fighting: true, LeftNinja: "test", Beads: []frame.Bead{{Label: "L1", Lit: true, Conf: .98}}}
}

func TestRecordingOnlySavesCurrentFightFramesAndResumesNextFight(t *testing.T) {
	r := testRecorder(t, true)
	base := time.Now()
	states := []struct {
		scene          string
		fighting, hold bool
		err            error
		wantRaw        string
	}{
		{"lobby", false, false, nil, "skipped_not_fighting"},
		{"queue", false, false, nil, "skipped_not_fighting"},
		{"pick", false, false, nil, "skipped_not_fighting"},
		{"vs", false, false, nil, "skipped_not_fighting"},
		{"fight", true, false, nil, "saved"},
		{"result", true, false, nil, "skipped_not_fighting"},
		{"fight", true, true, nil, "skipped_uncertain_scene"},
		{"", true, false, nil, "skipped_not_fighting"},
		{"fight", false, false, nil, "skipped_not_fighting"},
		{"fight", true, false, errors.New("capture failed"), "skipped_capture_error"},
		{"lobby", false, false, nil, "skipped_not_fighting"},
		{"fight", true, false, nil, "saved"},
	}
	for i, state := range states {
		at := base.Add(time.Duration(i) * 20 * time.Millisecond)
		f := validFrame(at)
		f.Scene, f.Fighting, f.Hold, f.Err = state.scene, state.fighting, state.hold, state.err
		r.Observe(f, at.Add(4*time.Millisecond))
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	s := r.Snapshot()
	if s.RecordedFrames != 2 || s.SkippedFrames != uint64(len(states)-2) || s.DroppedFrames != 0 || s.QueueBytes != 0 {
		t.Fatalf("fight-only recording counts: %+v", s)
	}
	files, err := os.ReadDir(filepath.Join(s.Directory, "frames"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("non-fight PNGs were saved: %d files", len(files))
	}
	rows := readJSONLines(t, filepath.Join(s.Directory, "frames.jsonl"))
	if len(rows) != len(states) {
		t.Fatalf("missing metadata boundaries: %d", len(rows))
	}
	for i, row := range rows {
		if row["rawState"] != states[i].wantRaw {
			t.Fatalf("frame %d: %+v", i, row)
		}
		if (row["image"] != "") != (states[i].wantRaw == "saved") {
			t.Fatalf("unexpected PNG reference at frame %d: %+v", i, row)
		}
	}
}

func draw(r *Recorder, id uint64, at time.Time) {
	r.MarkUI([]uint64{id}, "queued", at.Add(5*time.Millisecond))
	r.MarkUI([]uint64{id}, "applied", at.Add(7*time.Millisecond))
	r.MarkUI([]uint64{id}, "drawing", at.Add(8*time.Millisecond))
	r.MarkUI([]uint64{id}, "drawn", at.Add(10*time.Millisecond))
}

func TestMissingInvalidAndDuplicateFramesNeverBecomeE2E(t *testing.T) {
	r := testRecorder(t, false)
	base := time.Now()
	cases := []struct {
		name string
		edit func(*frame.Frame)
	}{
		{"no image", func(f *frame.Frame) { f.Img = nil }},
		{"empty image", func(f *frame.Frame) { f.Img = image.NewRGBA(image.Rectangle{}) }},
		{"held", func(f *frame.Frame) { f.Hold = true }},
		{"duplicate", func(f *frame.Frame) { f.Duplicate = true }},
		{"missing analysis", func(f *frame.Frame) { f.AnalysisStarted = time.Time{} }},
		{"backwards capture", func(f *frame.Frame) { f.CapturedAt = base.Add(-time.Second) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := validFrame(base)
			tc.edit(&f)
			id := r.Observe(f, base.Add(4*time.Millisecond))
			draw(r, id, base)
		})
	}
	report := r.report()
	if report.Status.Attempts != uint64(len(cases)) || report.Status.CompleteFrames != 0 {
		t.Fatalf("bad counts: %+v", report.Status)
	}
	d := report.Metrics["frame_end_to_end_ms"]
	if d.Count != 0 || d.P50MS != nil || d.P95MS != nil || d.P99MS != nil {
		t.Fatalf("missing sample must be null, got %+v", d)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{\"count\":0,\"p50_ms\":null,\"p95_ms\":null,\"p99_ms\":null,\"min_ms\":null,\"max_ms\":null}" {
		t.Fatalf("unexpected empty summary: %s", b)
	}
}

func TestPaintRequiredAndRepeatedPaintCountedOnce(t *testing.T) {
	r := testRecorder(t, false)
	base := time.Now()
	id := r.Observe(validFrame(base), base.Add(4*time.Millisecond))
	r.MarkUI([]uint64{id}, "queued", base.Add(5*time.Millisecond))
	r.MarkUI([]uint64{id}, "applied", base.Add(7*time.Millisecond))
	if r.report().Metrics["frame_end_to_end_ms"].Count != 0 {
		t.Fatal("UI post/applied must not be end-to-end")
	}
	r.MarkUI([]uint64{id}, "drawing", base.Add(8*time.Millisecond))
	r.MarkUI([]uint64{id}, "drawn", base.Add(10*time.Millisecond))
	draw(r, id, base.Add(time.Second))
	d := r.report().Metrics["frame_end_to_end_ms"]
	if d.Count != 1 || *d.P99MS != 10 {
		t.Fatalf("repeat paint changed latency: %+v", d)
	}
	if r.report().Metrics["ui_paint_submit_ms"].Count != 1 {
		t.Fatal("paint stage repeated")
	}
	id2 := r.Observe(validFrame(base.Add(time.Second)), base.Add(time.Second+4*time.Millisecond))
	r.MarkUI([]uint64{id2}, "queued", base) // Bad timestamps must not be repaired.
	draw(r, id2, base.Add(time.Second))
	if r.Snapshot().CompleteFrames != 1 {
		t.Fatal("backwards UI queue became E2E")
	}
}

func TestEventCorrelationSurvivesSupersedingFrame(t *testing.T) {
	r := testRecorder(t, false)
	base := time.Now()
	first := r.Observe(validFrame(base), base.Add(4*time.Millisecond))
	confirmBase := base.Add(20 * time.Millisecond)
	confirm := r.Observe(validFrame(confirmBase), confirmBase.Add(4*time.Millisecond))
	r.Confirm(Event{FrameID: confirm, Side: "right", Serial: 1, Number: 1, FirstObservedAt: base.Add(time.Millisecond), ConfirmedAt: confirmBase.Add(4 * time.Millisecond), UIVisible: true})
	carrierBase := base.Add(40 * time.Millisecond)
	carrier := r.Observe(validFrame(carrierBase), carrierBase.Add(4*time.Millisecond))
	r.CarryEvents(carrier, 0, 1)
	draw(r, carrier, carrierBase)
	report := r.report()
	if report.Status.CompleteFrames != 1 || report.Status.CompleteEvents != 1 {
		t.Fatalf("unexpected counts: %+v", report.Status)
	}
	if r.history[first].complete || r.history[confirm].complete {
		t.Fatal("superseded evidence/confirmation frame was falsely painted")
	}
	if got := *report.Metrics["event_end_to_end_ms"].P50MS; got != 50 {
		t.Fatalf("first evidence E2E=%v want50", got)
	}
	draw(r, confirm, confirmBase)
	if r.Snapshot().CompleteEvents != 1 {
		t.Fatal("event counted more than once")
	}
}

func TestHiddenAndRetroactivelyCarriedEventsExcluded(t *testing.T) {
	r := testRecorder(t, false)
	base := time.Now()
	id := r.Observe(validFrame(base), base.Add(4*time.Millisecond))
	r.Confirm(Event{FrameID: id, Side: "left", Serial: 1, FirstObservedAt: base.Add(time.Millisecond), ConfirmedAt: base.Add(4 * time.Millisecond), UIVisible: false})
	r.Confirm(Event{FrameID: id, Side: "right", Serial: 1, FirstObservedAt: base.Add(time.Millisecond), ConfirmedAt: base.Add(4 * time.Millisecond), UIVisible: true})
	if r.eventTraces[eventKey("left", 1)].firstFrameID != id {
		t.Fatal("hidden decision lost raw evidence correlation")
	}
	r.CarryEvents(id, 0, 0)
	draw(r, id, base)
	if r.Snapshot().CompleteEvents != 0 {
		t.Fatal("invisible event counted")
	}
	r.CarryEvents(id, 0, 1)
	if r.Snapshot().CompleteEvents != 0 {
		t.Fatal("already completed paint reused for newly displayed event")
	}
	if r.report().Metrics["event_confirmation_ms"].Count != 2 {
		t.Fatal("hidden event confirmation stage should still be observable")
	}
}

func TestMissingFirstEvidenceDoesNotInventEventLatency(t *testing.T) {
	r := testRecorder(t, false)
	base := time.Now()
	id := r.Observe(validFrame(base), base.Add(4*time.Millisecond))
	r.Confirm(Event{FrameID: id, Side: "right", Serial: 1, FirstObservedAt: base.Add(-time.Second), ConfirmedAt: base.Add(4 * time.Millisecond), UIVisible: true})
	draw(r, id, base)
	if r.report().Metrics["event_end_to_end_ms"].P50MS != nil {
		t.Fatal("event without recorded evidence acquired latency")
	}
}

func readJSONLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []map[string]any
	s := bufio.NewScanner(f)
	for s.Scan() {
		var row map[string]any
		if err := json.Unmarshal(s.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestCaptureHelperDiagnosticsJSONIsCorrelatedAndPrivate(t *testing.T) {
	r := testRecorder(t, false)
	base := time.Now()
	secretPath := `C:\Users\Private\AppData\Local\Temp\capture-abc\external_renderer_ipc.dll`
	uncPath := `\\server\share\MuMu\nx_main\MuMuManager.exe`
	requestID := "9a1b2c3d4e5f67890123456789abcdef"
	token := "token=very-private-helper-token"
	cases := []struct {
		outcome, reap, origin, terminal string
		revision                        uint64
	}{
		{"success", "completed", "", "", 1},
		{"timeout", "pending", "timeout", "pending", 2},
		{"busy", "not_needed", "timeout", "pending", 2},
		{"helper_launch", "not_needed", "", "", 3},
		{"helper_protocol", "completed", "", "", 4},
		{"sdk_worker", "completed", "", "", 5},
		{"timeout", "killed_reaped", "timeout", "killed_reaped", 6},
	}
	for i, tc := range cases {
		at := base.Add(time.Duration(i) * time.Millisecond)
		r.Observe(frame.Frame{Hold: true, Err: fmt.Errorf("helper stderr %s %s request_id=%s %s", secretPath, uncPath, requestID, token), CaptureStarted: at,
			Status: "助手输出 " + secretPath + " " + token, TextStatus: "采集超时，正在重试", TextError: "native " + uncPath,
			CaptureMethod:  `C:\Program Files\MuMu\mumu-helper.exe --request ` + requestID,
			SourceRevision: tc.revision, HelperRequestStart: at, HelperDeadline: at.Add(time.Second),
			HelperOutcome: tc.outcome, HelperReap: tc.reap, HelperOriginOutcome: tc.origin, HelperTerminal: tc.terminal}, at)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(r.Snapshot().Directory, "capture.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secretPath, uncPath, requestID, token, "helper stderr", "助手输出", "mumu-helper.exe"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("private diagnostic value leaked")
		}
	}
	rows := readJSONLines(t, filepath.Join(r.Snapshot().Directory, "capture.jsonl"))
	if len(rows) != len(cases) {
		t.Fatalf("rows=%d want %d", len(rows), len(cases))
	}
	for i, tc := range cases {
		row := rows[i]
		if row["helper_outcome"] != tc.outcome || row["helper_reap"] != tc.reap || row["source_revision"] != float64(tc.revision) || row["valid_for_latency"] != false || row["raw_state"] != "no_image" || row["error_category"] != tc.outcome || row["capture_method"] != "other" {
			t.Fatalf("row %d lost diagnostic correlation: %+v", i, row)
		}
		if row["status"] != nil || row["text_error"] != nil || row["text_status"] != "采集超时，正在重试" {
			t.Fatalf("row %d retained unsafe free text: %+v", i, row)
		}
		if tc.origin != "" && (row["helper_origin_outcome"] != tc.origin || row["helper_terminal"] != tc.terminal) {
			t.Fatalf("row %d lost origin/terminal: %+v", i, row)
		}
	}
}

func TestSanitizeCaptureDiagnosticPreservesOnlySafeCategoriesAndChineseStatus(t *testing.T) {
	category, detail := sanitizeCaptureDiagnostic(frame.Frame{Status: "空帧，等待画面"})
	if category != "no_image" || detail != "空帧，等待画面" {
		t.Fatalf("safe status = %q/%q", category, detail)
	}
	category, detail = sanitizeCaptureDiagnostic(frame.Frame{Status: "正在切换模拟器，请稍候"})
	if category != "source_switch" || detail != "正在切换模拟器，请稍候" {
		t.Fatalf("source switch = %q/%q", category, detail)
	}
	category, detail = sanitizeCaptureDiagnostic(frame.Frame{Err: errors.New(`C:\Temp\secret.exe`)})
	if category != "no_image" || detail != "" {
		t.Fatalf("unsafe error = %q/%q", category, detail)
	}
}

func TestNativePNGIsImmutableAndReplayHasCorrelatedRecognition(t *testing.T) {
	r := testRecorder(t, true)
	base := time.Now()
	f := validFrame(base)
	f.Img = image.NewRGBA(image.Rect(7, 9, 10, 11))
	want := color.RGBA{R: 12, G: 34, B: 56, A: 255}
	f.Img.SetRGBA(7, 9, want)
	id := r.Observe(f, base.Add(4*time.Millisecond))
	f.Img.SetRGBA(7, 9, color.RGBA{R: 255, A: 255})
	f.Beads[0].Label = "mutated"
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	status := r.Snapshot()
	if !status.Finalized || status.RecordedFrames != 1 || status.QueueBytes != 0 {
		t.Fatalf("not drained: %+v", status)
	}
	rows := readJSONLines(t, filepath.Join(status.Directory, "frames.jsonl"))
	if len(rows) != 1 {
		t.Fatalf("replay rows %d", len(rows))
	}
	if uint64(rows[0]["index"].(float64)) != id {
		t.Fatal("replay id mismatch")
	}
	file, err := os.Open(filepath.Join(status.Directory, rows[0]["image"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 3 || img.Bounds().Dy() != 2 || color.RGBAModel.Convert(img.At(0, 0)) != want {
		t.Fatalf("native pixels changed: %v %v", img.Bounds(), img.At(0, 0))
	}
	captures := readJSONLines(t, filepath.Join(status.Directory, "capture.jsonl"))
	var captured map[string]any
	for _, entry := range captures {
		if entry["type"] == "capture" && entry["frame_id"] == float64(id) {
			captured = entry
			break
		}
	}
	if captured == nil || captured["beads"].([]any)[0].(map[string]any)["Label"] != "L1" {
		t.Fatal("recognition metadata was not copied")
	}
	if captured["origin_x"].(float64) != 7 {
		t.Fatal("original image origin missing")
	}
	if r.Observe(f, time.Now()) != 0 {
		t.Fatal("accepted frame after close")
	}
}

func TestRecordingLimitsHaveExplicitDropsAndNoFakePaths(t *testing.T) {
	for _, tc := range []struct {
		name          string
		bytes, queued int64
	}{
		{"pixel queue", 1 << 20, 1}, {"disk", 1, 1 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New(Options{Root: t.TempDir(), RecordFrames: true, MaxRecordingBytes: tc.bytes, MaxQueuedBytes: tc.queued})
			if err != nil {
				t.Fatal(err)
			}
			base := time.Now()
			r.Observe(validFrame(base), base.Add(4*time.Millisecond))
			r.Observe(frame.Frame{CaptureStarted: base, Hold: true}, base.Add(10*time.Millisecond))
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			status := r.Snapshot()
			if status.DroppedFrames != 1 || status.RecordedFrames != 0 || status.RecordingBytes != 0 || status.QueueBytes != 0 {
				t.Fatalf("bad drop accounting: %+v", status)
			}
			rows := readJSONLines(t, filepath.Join(status.Directory, "frames.jsonl"))
			if len(rows) != 2 {
				t.Fatalf("dropped/missing capture silently omitted: %d", len(rows))
			}
			for _, row := range rows {
				if row["image"] != "" || row["error"] == "" {
					t.Fatalf("fake path or missing drop error: %+v", row)
				}
			}
			files, err := os.ReadDir(filepath.Join(status.Directory, "frames"))
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 0 {
				t.Fatal("partial PNG leaked")
			}
		})
	}
}

func TestSlowPNGDoesNotBlockCaptureAndEventLogs(t *testing.T) {
	r := testRecorder(t, true)
	encoding, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once
	releaseEncoder := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseEncoder)
	r.encodePNG = func(w io.Writer, img *image.RGBA) error {
		once.Do(func() { close(encoding) })
		<-release
		return png.Encode(w, img)
	}
	base := time.Now()
	id := r.Observe(validFrame(base), base.Add(4*time.Millisecond))
	<-encoding
	r.Confirm(Event{FrameID: id, Side: "right", Serial: 1, Number: 1,
		FirstObservedAt: base.Add(time.Millisecond), ConfirmedAt: base.Add(4 * time.Millisecond), UIVisible: true})
	draw(r, id, base)
	// More UI stages than the old shared queue's 256 entries, while the PNG
	// encoder is deliberately blocked. The log worker must keep draining them.
	for i := 1; i <= 80; i++ {
		at := base.Add(time.Duration(i) * 20 * time.Millisecond)
		f := validFrame(at)
		f.Fighting, f.Scene = false, "lobby"
		draw(r, r.Observe(f, at.Add(4*time.Millisecond)), at)
	}
	path := filepath.Join(r.Snapshot().Directory, "capture.jsonl")
	deadline := time.Now().Add(3 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"type":"event_confirmed"`) && strings.Contains(string(data), `"frame_id":81`) {
			found = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !found {
		t.Fatal("PNG encoding blocked capture/event logs")
	}
	if s := r.Snapshot(); s.DroppedLogs != 0 || s.RecordedFrames != 0 {
		t.Fatalf("unexpected blocked-encoder status: %+v", s)
	}
	releaseEncoder()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if r.Snapshot().RecordedFrames != 1 {
		t.Fatal("PNG not drained at close")
	}
}

func TestBoundedSessionAndConcurrentCloseExport(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), MaxObservations: 2})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for i := 0; i < 4; i++ {
		r.Observe(validFrame(base), base.Add(4*time.Millisecond))
	}
	if len(r.history) != 2 || r.Snapshot().DroppedLogs != 2 {
		t.Fatal("session memory limit not enforced")
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.ExportReport(); err != nil {
				t.Error(err)
			}
			if err := r.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if !r.Snapshot().Finalized {
		t.Fatal("final report not complete")
	}
	var report Report
	b, err := os.ReadFile(filepath.Join(r.Snapshot().Directory, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Status.Finalized || report.RejectedObservations != 2 {
		t.Fatalf("bad report: %+v", report.Status)
	}
}

func TestNearestRankPercentiles(t *testing.T) {
	values := make([]float64, 100)
	for i := range values {
		values[i] = float64(100 - i)
	}
	d := summarize(values)
	if *d.P50MS != 50 || *d.P95MS != 95 || *d.P99MS != 99 {
		t.Fatalf("wrong percentiles: %+v", d)
	}
}
