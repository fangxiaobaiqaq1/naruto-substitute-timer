package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/draw"
	_ "image/png"
	"os"
	"path/filepath"
	"time"

	"narutotimer/internal/app"
	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

type replayResult struct {
	Index         int     `json:"index"`
	OffsetMS      float64 `json:"offsetMs"`
	Image         string  `json:"image,omitempty"`
	Scene         string  `json:"scene"`
	LayoutProfile string  `json:"layoutProfile,omitempty"`
	Engine        string  `json:"engine"`
	Hold          bool    `json:"hold"`
	Duplicate     bool    `json:"duplicate"`
	MissingBefore int     `json:"missingSamplesBefore,omitempty"`
	Left          *int    `json:"left"`
	Right         *int    `json:"right"`
	AnalyzeMS     float64 `json:"analyzeMs"`
	Error         string  `json:"error,omitempty"`
}

func replay(args []string) error {
	f := flag.NewFlagSet("replay", flag.ContinueOnError)
	manifest := f.String("manifest", "", "frames.jsonl recorded with capture -record")
	imagePath := f.String("image", "", "single PNG for detector and latency inspection")
	repeat := f.Int("repeat", 1, "repeat a single image for timing; duplicates never vote twice")
	configPath := f.String("config", "config.json", "configuration path")
	out := f.String("out", "", "new output directory, required")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *out == "" || (*manifest == "") == (*imagePath == "") || *repeat < 1 {
		return fmt.Errorf("choose exactly one of -manifest or -image; -out required; repeat >=1")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		return err
	}
	mode := factory.FromApp(cfg).Mode
	if _, err := os.Stat(*out); !os.IsNotExist(err) {
		return fmt.Errorf("output directory must not exist: %s", *out)
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	var samples []sample
	dir := "."
	if *manifest != "" {
		file, err := os.Open(*manifest)
		if err != nil {
			return err
		}
		defer file.Close()
		dir = filepath.Dir(*manifest)
		scan := bufio.NewScanner(file)
		scan.Buffer(make([]byte, 4096), 1024*1024)
		for scan.Scan() {
			var s sample
			if err := json.Unmarshal(scan.Bytes(), &s); err != nil {
				return err
			}
			samples = append(samples, s)
		}
		if err := scan.Err(); err != nil {
			return err
		}
	} else {
		abs, err := filepath.Abs(*imagePath)
		if err != nil {
			return err
		}
		dir = filepath.Dir(abs)
		for i := 0; i < *repeat; i++ {
			samples = append(samples, sample{Index: i + 1, Image: filepath.Base(abs), OffsetMS: float64(i * 33)})
		}
	}
	if len(samples) == 0 {
		return fmt.Errorf("no recorded samples")
	}
	observations, err := os.Create(filepath.Join(*out, "observations.jsonl"))
	if err != nil {
		return err
	}
	defer observations.Close()
	obsEnc := json.NewEncoder(observations)
	events, err := os.Create(filepath.Join(*out, "events.jsonl"))
	if err != nil {
		return err
	}
	defer events.Close()
	eventEnc := json.NewEncoder(events)
	base := time.Unix(1700000000, 0)
	var previous [32]byte
	havePrevious := false
	var sequence replaySequence
	var costs []float64
	unknown, duplicates, eventCount := 0, 0, 0
	sampleGaps, missingSamples := 0, 0
	cd := ninja.DefaultCooldown
	tracker := newReplayTracker(cfg, cd)
	for _, s := range samples {
		missing, err := sequence.advance(s)
		if err != nil {
			return err
		}
		if missing > 0 {
			tracker.resyncAfterRecordingGap()
			sampleGaps++
			missingSamples += missing
		}
		at := base.Add(time.Duration(s.OffsetMS * float64(time.Millisecond)))
		r := replayResult{Index: s.Index, Image: s.Image, OffsetMS: s.OffsetMS, MissingBefore: missing}
		if s.Error != "" || s.Image == "" {
			r.Hold = true
			r.Error = s.Error
			if r.Error == "" {
				r.Error = "missing image; use capture -record"
			}
			tracker.observe(frame.Frame{Hold: true, CapturedAt: at})
			unknown++
			if err := obsEnc.Encode(r); err != nil {
				return err
			}
			continue
		}
		// A recording references only files inside its own directory.
		if !filepath.IsLocal(s.Image) {
			return fmt.Errorf("non-local recording image path %q", s.Image)
		}
		img, err := loadRGBA(filepath.Join(dir, s.Image))
		if err != nil {
			return err
		}
		hash := sha256.Sum256(img.Pix)
		r.Duplicate = s.Duplicate || (havePrevious && hash == previous)
		previous = hash
		havePrevious = true
		beg := time.Now()
		fr := frame.AnalyzeImage(img, eng, mode, at, at, "replay")
		fr.Duplicate = r.Duplicate
		r.AnalyzeMS = float64(time.Since(beg)) / float64(time.Millisecond)
		costs = append(costs, r.AnalyzeMS)
		r.Scene, r.Engine, r.Hold = fr.Scene, fr.Engine, fr.Hold
		r.LayoutProfile = fr.LayoutProfile
		r.Left = knownCount(fr.Beads, 'L', fr.Slots(true, cfg.Layout.BeadsPerSide))
		r.Right = knownCount(fr.Beads, 'R', fr.Slots(false, cfg.Layout.BeadsPerSide))
		if fr.Err != nil {
			r.Error = fr.Err.Error()
		}
		if r.Left == nil || r.Right == nil {
			unknown++
		}
		if r.Duplicate {
			duplicates++
		}
		for _, event := range tracker.observe(fr) {
			eventCount++
			if err := eventEnc.Encode(map[string]any{"side": event.side, "eventNumber": event.number, "firstObservedMs": float64(event.firstObserved.Sub(base)) / float64(time.Millisecond), "confirmedMs": s.OffsetMS, "confirmationMs": float64(at.Sub(event.firstObserved)) / float64(time.Millisecond), "cooldownSeconds": cd.Seconds()}); err != nil {
				return err
			}
		}
		if err := obsEnc.Encode(r); err != nil {
			return err
		}
	}
	report := map[string]any{"samples": len(samples), "duplicates": duplicates, "samplesWithUnknownSide": unknown, "sampleGaps": sampleGaps, "missingSamples": missingSamples, "events": eventCount, "analysisP50Ms": percentile(costs, .5), "analysisP95Ms": percentile(costs, .95), "analysisP99Ms": percentile(costs, .99), "note": "No ground truth labels supplied: these are detections and processing times, not measured accuracy or screen-to-overlay latency. Missing manifest indices reset observation baselines; events during the gaps cannot be inferred."}
	b, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(filepath.Join(*out, "summary.json"), b, 0644); err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// Both capture attempts and diagnostics frame IDs are positive and monotonic.
// A manifest may start midway through a session, so its first index need not be 1.
// Missing entries are evidence boundaries even when their timestamps are close.
type replaySequence struct {
	previousIndex  int
	previousOffset float64
}

func (s *replaySequence) advance(next sample) (int, error) {
	if next.Index <= 0 {
		return 0, fmt.Errorf("recording sample index must be positive: %d", next.Index)
	}
	if s.previousIndex != 0 && next.Index <= s.previousIndex {
		return 0, fmt.Errorf("recording sample indices must strictly increase: %d after %d", next.Index, s.previousIndex)
	}
	if s.previousIndex != 0 && next.OffsetMS < s.previousOffset {
		return 0, fmt.Errorf("recording timestamps go backwards at sample %d", next.Index)
	}
	missing := 0
	if s.previousIndex != 0 {
		missing = next.Index - s.previousIndex - 1
	}
	s.previousIndex, s.previousOffset = next.Index, next.OffsetMS
	return missing, nil
}

// replayTracker mirrors the UI session's temporal rules, without display state.
// Keep scene admission, hold resynchronization and profile resets aligned with
// internal/ui/run.go; both paths feed only raw, independent evidence to SideClock.
type replayTracker struct {
	cfg                   config.Config
	cd                    time.Duration
	left, right           app.SideClock
	fighting, holdFreeze  bool
	inheritOnReturn       bool
	syncLeft, syncRight   bool
	fightHits, leaveHits  int
	sceneObservedAt       time.Time
	layoutProfile         string
	leftSlots, rightSlots int
	observationSpace      app.ObservationSpace
}

type replayEvent struct {
	side          string
	firstObserved time.Time
	number        uint64
}

func newReplayTracker(cfg config.Config, cd time.Duration) *replayTracker {
	t := &replayTracker{cfg: cfg, cd: cd}
	gap := max(150*time.Millisecond, time.Duration(cfg.UI.PollIntervalMS*3)*time.Millisecond)
	t.left.SetObservationGap(gap)
	t.right.SetObservationGap(gap)
	return t
}

func (t *replayTracker) invalidate(at time.Time) {
	t.left.InvalidateObservation(at)
	t.right.InvalidateObservation(at)
}

func (t *replayTracker) resyncAfterRecordingGap() {
	t.left.ResyncObservation()
	t.right.ResyncObservation()
	t.fightHits, t.leaveHits = 0, 0
	t.syncLeft, t.syncRight = false, false
	t.inheritOnReturn = false
}

func (t *replayTracker) observe(f frame.Frame) []replayEvent {
	width, height := 0, 0
	if f.Img != nil {
		width, height = f.Img.Bounds().Dx(), f.Img.Bounds().Dy()
	}
	if t.observationSpace.Update(width, height, f.CaptureMethod) {
		t.left.ResyncObservation()
		t.right.ResyncObservation()
	}
	if f.Hold || f.Err != nil {
		if f.Err != nil && (t.holdFreeze || t.syncLeft || t.syncRight) {
			t.left.ResyncObservation()
			t.right.ResyncObservation()
		}
		t.leaveHits = 0
		t.invalidate(f.CapturedAt)
		if contains(t.cfg.Scene.HoldScenes, f.Scene) {
			t.inheritOnReturn = f.Scene == "vs" && t.fighting
			t.holdFreeze = true
		}
		return nil
	}
	if f.Fighting && f.LayoutProfile != "" {
		if t.layoutProfile != "" && t.layoutProfile != f.LayoutProfile {
			t.left.Reset()
			t.right.Reset()
			t.fighting, t.holdFreeze = false, false
			t.fightHits, t.leaveHits = 0, 0
			t.syncLeft, t.syncRight = false, false
		}
		t.layoutProfile = f.LayoutProfile
	}
	leftSlots, rightSlots := f.Slots(true, t.cfg.Layout.BeadsPerSide), f.Slots(false, t.cfg.Layout.BeadsPerSide)
	if t.leftSlots != 0 && t.leftSlots != leftSlots {
		t.left.ResyncObservation()
	}
	if t.rightSlots != 0 && t.rightSlots != rightSlots {
		t.right.ResyncObservation()
	}
	t.leftSlots, t.rightSlots = leftSlots, rightSlots
	left := knownCount(f.Beads, 'L', f.Slots(true, t.cfg.Layout.BeadsPerSide))
	right := knownCount(f.Beads, 'R', f.Slots(false, t.cfg.Layout.BeadsPerSide))
	t.applyScene(f, left, right)
	if !t.fighting || !f.Fighting {
		t.invalidate(f.CapturedAt)
		return nil
	}
	if f.Duplicate {
		return nil
	}
	var events []replayEvent
	for _, side := range []struct {
		name  string
		clock *app.SideClock
		ready *int
		sync  *bool
	}{{"left", &t.left, left, &t.syncLeft}, {"right", &t.right, right, &t.syncRight}} {
		if side.ready == nil {
			side.clock.InvalidateObservation(f.CapturedAt)
			continue
		}
		if *side.sync {
			if t.inheritOnReturn {
				*side.sync = !side.clock.ResumeInheritedObservation(f.CapturedAt)
			} else {
				side.clock.SyncReady(*side.ready)
				*side.sync = false
			}
		}
		_, before := side.clock.LastEvent()
		side.clock.Observe(*side.ready, true, f.CapturedAt, t.cd, t.cfg.Tracking.MinimumConfirmFrames)
		first, after := side.clock.LastEvent()
		if after != before {
			events = append(events, replayEvent{side: side.name, firstObserved: first, number: side.clock.EventCount()})
		}
	}
	return events
}

func (t *replayTracker) applyScene(f frame.Frame, left, right *int) {
	if !f.CapturedAt.IsZero() {
		if !t.sceneObservedAt.IsZero() && !f.CapturedAt.After(t.sceneObservedAt) {
			t.leaveHits = 0
			return
		}
		t.sceneObservedAt = f.CapturedAt
	}
	if f.Hold || f.Err != nil || !contains(t.cfg.Scene.EndScenes, f.Scene) {
		t.leaveHits = 0
	}
	if contains(t.cfg.Scene.HoldScenes, f.Scene) {
		t.inheritOnReturn = f.Scene == "vs" && t.fighting
		t.holdFreeze = true
		return
	}
	if f.Fighting {
		if t.holdFreeze {
			t.syncLeft, t.syncRight = true, true
			t.holdFreeze = false
		}
		t.leaveHits = 0
		t.fightHits++
		if t.fightHits >= max(1, t.cfg.Tracking.EnterFightFrames) {
			t.fighting = true
		}
		return
	}
	if contains(t.cfg.Scene.EndScenes, f.Scene) {
		t.fightHits = 0
		if t.fighting {
			t.leaveHits++
			if t.leaveHits >= max(10, t.cfg.Tracking.LeaveFightFrames) {
				t.fighting = false
				t.left.Reset()
				t.right.Reset()
				t.syncLeft, t.syncRight = false, false
			}
		}
	}
}

func loadRGBA(path string) (*image.RGBA, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	img, _, e := image.Decode(f)
	if e != nil {
		return nil, e
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out, nil
}
func knownCount(beads []frame.Bead, side byte, expected int) *int {
	if expected <= 0 {
		return nil
	}
	n, count := 0, 0
	for _, b := range beads {
		if len(b.Label) == 0 || b.Label[0] != side {
			continue
		}
		n++
		ready := b.Lit || b.Gold
		if b.Unknown || ready == b.Dark {
			return nil
		}
		if ready {
			count++
		}
	}
	if n != expected {
		return nil
	}
	return &count
}
func contains(list []string, s string) bool {
	if s == "" {
		return false
	}
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
