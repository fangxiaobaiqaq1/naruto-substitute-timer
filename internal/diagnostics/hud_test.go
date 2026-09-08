package diagnostics

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/frame"
)

func hudTestFrame(at time.Time) frame.Frame {
	f := validFrame(at)
	f.Img = image.NewRGBA(image.Rect(7, 11, 967, 551))
	f.Beads = []frame.Bead{{Label: "L1", X: 107, Y: 111, Unknown: true}, {Label: "R1", X: 867, Y: 111, Unknown: true}}
	// Name/bean strip compresses cheaply; the rest deliberately exceeds the
	// full-frame budget. A successful HUD save must not depend on a full PNG.
	state := uint32(1)
	for y := 160; y < 551; y++ {
		for x := 7; x < 967; x++ {
			state = state*1664525 + 1013904223
			f.Img.SetRGBA(x, y, color.RGBA{uint8(state), uint8(state >> 8), uint8(state >> 16), 255})
		}
	}
	f.Img.SetRGBA(107, 111, color.RGBA{181, 59, 223, 255})
	return f
}

func waitRecordedWork(t *testing.T, r *Recorder) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r.Snapshot().QueueBytes == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("image worker did not drain")
}

func TestHUDEvidenceSurvivesFullRecordingLimitAndKeepsOriginalPixels(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), RecordFrames: true, RecordHUDEvidence: true, MaxRecordingBytes: 16 << 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	base := time.Now()
	f := hudTestFrame(base)
	region := hudRegion(f)
	r.Observe(f, base.Add(4*time.Millisecond))
	f.Img.SetRGBA(107, 111, color.RGBA{255, 0, 0, 255})
	waitRecordedWork(t, r)
	if s := r.Snapshot(); s.RecordedFrames != 0 || s.DroppedFrames != 1 || s.HUDFrames != 1 {
		t.Fatalf("expected full PNG limit with preserved HUD: %+v", s)
	}
	// The second HUD is still written after full recording has stopped.
	r.Observe(hudTestFrame(base.Add(time.Second)), base.Add(time.Second+4*time.Millisecond))
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	s := r.Snapshot()
	if s.HUDFrames != 2 || s.DroppedHUD != 0 || s.HUDBytes != s.RecordingBytes || s.RecordingBytes > 16<<10 || s.QueueBytes != 0 {
		t.Fatalf("reserved recording budget: %+v", s)
	}
	file, err := os.Open(filepath.Join(s.Directory, "hud", "000000001.png"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Size() != region.Size() {
		t.Fatalf("HUD resized: %v, want %v", decoded.Bounds(), region)
	}
	got := color.RGBAModel.Convert(decoded.At(107-region.Min.X, 111-region.Min.Y)).(color.RGBA)
	if got != (color.RGBA{181, 59, 223, 255}) {
		t.Fatalf("HUD pixels changed with caller buffer: %+v", got)
	}
	for _, row := range readJSONLines(t, filepath.Join(s.Directory, "frames.jsonl")) {
		if row["image"] != "" {
			t.Fatalf("HUD passed off as a full replay frame: %+v", row)
		}
	}
	var hudRows int
	for _, row := range readJSONLines(t, filepath.Join(s.Directory, "capture.jsonl")) {
		if row["type"] == "hud_frame" {
			hudRows++
			if row["crop"] == nil || row["frame_id"] == nil || row["source_width"] != float64(960) {
				t.Fatalf("HUD source correlation missing: %+v", row)
			}
		}
	}
	if hudRows != 2 {
		t.Fatal("missing HUD save records")
	}
}

func TestHUDEvidenceFightGateSamplingAndLimit(t *testing.T) {
	r, err := New(Options{Root: t.TempDir(), RecordFrames: true, RecordHUDEvidence: true, MaxRecordingBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for i, state := range []string{"lobby", "hold", "fight", "fight", "result", "fight"} {
		at := base.Add(time.Duration(i) * 200 * time.Millisecond)
		f := hudTestFrame(at)
		f.Scene, f.Fighting, f.Hold = state, state == "fight", state == "hold"
		r.Observe(f, at.Add(4*time.Millisecond))
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	s := r.Snapshot()
	if s.HUDFrames != 0 || s.RecordingBytes != 0 || s.DroppedHUD != 2 || s.QueueBytes != 0 {
		t.Fatalf("HUD gate/rate/limit: %+v", s)
	}
	files, err := os.ReadDir(filepath.Join(s.Directory, "hud"))
	if err != nil || len(files) != 0 {
		t.Fatalf("non-fight/partial HUD files: %v %v", files, err)
	}
	var states []string
	for _, row := range readJSONLines(t, filepath.Join(s.Directory, "capture.jsonl")) {
		if row["type"] == "capture" {
			states = append(states, row["hud_state"].(string))
		}
	}
	if len(states) != 6 || states[0] != "skipped_not_fighting" || states[1] != "skipped_uncertain_scene" || states[3] != "sampling_interval" || states[4] != "skipped_not_fighting" {
		t.Fatalf("wrong HUD policy: %v", states)
	}
}
