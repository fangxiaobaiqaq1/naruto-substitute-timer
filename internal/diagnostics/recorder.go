// Package diagnostics records capture evidence and correlated latency stages.
// It deliberately has no wall-clock/function-duration fallback for missing frames.
package diagnostics

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"narutotimer/internal/frame"
)

const (
	defaultRecordingBytes = int64(1 << 30)
	defaultQueuedBytes    = int64(64 << 20)
	maxLogBytes           = int64(64 << 20)
	defaultObservations   = 100000
	maxHistory            = 4096
)

type Options struct {
	Root              string
	RecordFrames      bool
	RecordHUDEvidence bool // Native-pixel name/bean strip at 2 Hz, within the same disk limit.
	Config            any
	MaxRecordingBytes int64
	MaxQueuedBytes    int64
	MaxObservations   int
}

// Event correlates a confirmed decision with the frame containing that decision
// and its first evidence. FirstObservedAt must be that evidence's CapturedAt.
type Event struct {
	FrameID         uint64    `json:"frame_id"`
	Side            string    `json:"side"`
	Serial          uint64    `json:"serial"`
	Number          uint64    `json:"number"`
	FirstObservedAt time.Time `json:"first_observed_at"`
	ConfirmedAt     time.Time `json:"confirmed_at"`
	UIVisible       bool      `json:"ui_visible"`
}

type Status struct {
	Directory      string `json:"directory"`
	Attempts       uint64 `json:"attempts"`
	ValidFrames    uint64 `json:"valid_frames"`
	RecordedFrames uint64 `json:"recorded_frames"`
	HUDFrames      uint64 `json:"hud_frames"`
	DroppedHUD     uint64 `json:"dropped_hud_frames"`
	HUDBytes       int64  `json:"hud_bytes"`
	SkippedFrames  uint64 `json:"skipped_frames"`
	DroppedFrames  uint64 `json:"dropped_frames"`
	DroppedLogs    uint64 `json:"dropped_logs"`
	CompleteFrames uint64 `json:"complete_frames"`
	CompleteEvents uint64 `json:"complete_events"`
	RecordingBytes int64  `json:"recording_bytes"`
	QueueBytes     int64  `json:"queue_bytes"`
	LastError      string `json:"last_error,omitempty"`
	Closed         bool   `json:"closed"`
	Finalized      bool   `json:"finalized"`
}

type observation struct {
	Type                string       `json:"type"`
	FrameID             uint64       `json:"frame_id"`
	SourceSequence      uint64       `json:"source_sequence"`
	CaptureStarted      time.Time    `json:"capture_started"`
	CapturedAt          time.Time    `json:"captured_at"`
	AnalysisStarted     time.Time    `json:"analysis_started"`
	AnalyzedAt          time.Time    `json:"analyzed_at"`
	ReceivedAt          time.Time    `json:"received_at"`
	CaptureMethod       string       `json:"capture_method"`
	Width               int          `json:"width"`
	Height              int          `json:"height"`
	OriginX             int          `json:"origin_x"`
	OriginY             int          `json:"origin_y"`
	Valid               bool         `json:"valid_for_latency"`
	Exclusion           string       `json:"exclusion,omitempty"`
	RawState            string       `json:"raw_state"`
	HUDState            string       `json:"hud_state,omitempty"`
	Duplicate           bool         `json:"duplicate"`
	Hold                bool         `json:"hold"`
	Error               string       `json:"error,omitempty"`
	Status              string       `json:"status,omitempty"`
	TextStatus          string       `json:"text_status,omitempty"`
	TextError           string       `json:"text_error,omitempty"`
	Fighting            bool         `json:"fighting"`
	Engine              string       `json:"engine"`
	Scene               string       `json:"scene"`
	LayoutProfile       string       `json:"layout_profile"`
	LeftNinja           string       `json:"left_ninja"`
	RightNinja          string       `json:"right_ninja"`
	LeftNinjaCandidate  string       `json:"left_ninja_candidate,omitempty"`
	RightNinjaCandidate string       `json:"right_ninja_candidate,omitempty"`
	LeftSlots           int          `json:"left_slots"`
	RightSlots          int          `json:"right_slots"`
	PlayerSide          string       `json:"player_side"`
	PlayerName          string       `json:"player_name"`
	OpponentName        string       `json:"opponent_name"`
	Beads               []frame.Bead `json:"beads"`
}

type trace struct {
	id                                                  uint64
	captureStarted, captured, analysisStarted, analyzed time.Time
	received, queued, applied, drawing, drawn           time.Time
	valid, complete                                     bool
	counted                                             map[string]bool
	events                                              []*eventTrace
}

type eventTrace struct {
	event         Event
	firstFrameID  uint64
	firstCapture  time.Time
	firstCaptured time.Time
	valid         bool
	complete      bool
	exclusion     string
}

type work struct {
	entry    any
	img      *image.RGBA
	frameID  uint64
	bytes    int64
	hud      *image.RGBA
	hudBytes int64
}

// Recorder is safe for concurrent capture, UI, export and close calls. PNG
// compression and metadata use separate bounded workers. The caller's pixels are
// copied before Observe returns; it may immediately reuse its capture buffer.
type Recorder struct {
	mu            sync.Mutex
	exportMu      sync.Mutex
	logMu         sync.Mutex
	opts          Options
	status        Status
	started       time.Time
	ended         time.Time
	queue         chan work
	frameQueue    chan work
	framesDone    chan struct{}
	encodePNG     func(io.Writer, *image.RGBA) error
	done          chan struct{}
	log           *os.File
	framesLog     *os.File
	logBytes      int64
	recordingFull bool
	hudFull       bool
	lastHUDAt     time.Time
	history       map[uint64]*trace
	ring          []uint64
	ringNext      int
	samples       map[string][]float64
	exclusions    map[string]uint64
	eventKeys     map[string]bool
	eventTraces   map[string]*eventTrace
	events        uint64
	rejected      uint64
	closeErr      error
}

func New(opts Options) (*Recorder, error) {
	if opts.Root == "" {
		opts.Root = "diagnostics"
	}
	if opts.MaxRecordingBytes <= 0 {
		opts.MaxRecordingBytes = defaultRecordingBytes
	}
	if opts.MaxQueuedBytes <= 0 {
		opts.MaxQueuedBytes = defaultQueuedBytes
	}
	if opts.MaxObservations <= 0 {
		opts.MaxObservations = defaultObservations
	}
	// A session has a deliberate upper bound even if the caller supplies a typo.
	if opts.MaxObservations > defaultObservations {
		opts.MaxObservations = defaultObservations
	}
	opts.RecordHUDEvidence = opts.RecordHUDEvidence && opts.RecordFrames
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	started := time.Now()
	dir, err := os.MkdirTemp(root, "session-"+started.Format("20060102-150405")+"-")
	if err != nil {
		return nil, err
	}
	if opts.RecordFrames {
		if err = os.Mkdir(filepath.Join(dir, "frames"), 0755); err != nil {
			return nil, err
		}
	}
	if opts.RecordHUDEvidence {
		if err = os.Mkdir(filepath.Join(dir, "hud"), 0755); err != nil {
			return nil, err
		}
	}
	manifest := map[string]any{
		"schema_version": 1, "started_at": started, "record_frames": opts.RecordFrames,
		"max_recording_bytes": opts.MaxRecordingBytes, "max_queued_bytes": opts.MaxQueuedBytes,
		"max_observations": opts.MaxObservations, "max_log_bytes": maxLogBytes,
		"timestamp_clock":       "Go monotonic time for in-process durations; RFC3339 wall time in logs",
		"capture_origin":        "capture API request; source/game presentation timestamp unavailable",
		"draw_endpoint":         "paint and SwapBuffers completion when instrumented; physical display time unavailable",
		"raw_format":            "lossless native-size PNG, without UI overlays; original origin in capture.jsonl",
		"raw_frame_policy":      "only current fighting scene frames without hold or errors; other scenes retain metadata only",
		"hud_evidence":          opts.RecordHUDEvidence,
		"hud_evidence_policy":   "when enabled, reserve 1/4 of the same PNG byte limit for native-pixel name/bean strips at most twice per second; not full frames or replay inputs; crop coordinates and frame_id in capture.jsonl",
		"player_side_semantics": "per-frame identity observation; an empty player_side may mean no new sample during the identity sampling interval, not loss of the UI's remembered side",
		"config":                opts.Config,
	}
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("diagnostics config: %w", err)
	}
	if err = os.WriteFile(filepath.Join(dir, "session.json"), b, 0644); err != nil {
		return nil, err
	}
	log, err := os.Create(filepath.Join(dir, "capture.jsonl"))
	if err != nil {
		return nil, err
	}
	r := &Recorder{opts: opts, status: Status{Directory: dir}, started: started,
		queue: make(chan work, 4096), frameQueue: make(chan work, 256), framesDone: make(chan struct{}), done: make(chan struct{}), log: log,
		encodePNG: func(w io.Writer, img *image.RGBA) error {
			return (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(w, img)
		},
		history: make(map[uint64]*trace), samples: make(map[string][]float64),
		exclusions: make(map[string]uint64), eventKeys: make(map[string]bool), eventTraces: make(map[string]*eventTrace)}
	if opts.RecordFrames {
		r.framesLog, err = os.Create(filepath.Join(dir, "frames.jsonl"))
		if err != nil {
			_ = log.Close()
			return nil, err
		}
	}
	go r.runFrames()
	go r.run()
	return r, nil
}

// Observe assigns an ID even to failed captures, so failed attempts are visible
// in the log and cannot accidentally become zero-duration latency samples.
func (r *Recorder) Observe(f frame.Frame, receivedAt time.Time) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.Closed {
		return 0
	}
	r.status.Attempts++
	id := r.status.Attempts
	if id > uint64(r.opts.MaxObservations) {
		r.rejected++
		r.status.DroppedLogs++
		if r.opts.RecordFrames && usableImage(f.Img) {
			if rawRecordingSkip(f) != "" {
				r.status.SkippedFrames++
			} else {
				r.status.DroppedFrames++
			}
		}
		r.status.LastError = "session observation limit reached; start a new diagnostic session"
		return id
	}
	t := &trace{id: id, captureStarted: f.CaptureStarted, captured: f.CapturedAt,
		analysisStarted: f.AnalysisStarted, analyzed: f.AnalyzedAt, received: receivedAt,
		counted: make(map[string]bool)}
	exclusion := invalidFrame(f, receivedAt)
	t.valid = exclusion == ""
	if t.valid {
		r.status.ValidFrames++
		r.sampleLocked("capture_ms", t.captureStarted, t.captured)
		r.sampleLocked("analysis_wait_ms", t.captured, t.analysisStarted)
		r.sampleLocked("analysis_ms", t.analysisStarted, t.analyzed)
		r.sampleLocked("analysis_to_received_ms", t.analyzed, t.received)
	} else {
		r.exclusions[exclusion]++
	}
	r.addHistoryLocked(t)
	o := observation{Type: "capture", FrameID: id, SourceSequence: f.Sequence,
		CaptureStarted: f.CaptureStarted, CapturedAt: f.CapturedAt,
		AnalysisStarted: f.AnalysisStarted, AnalyzedAt: f.AnalyzedAt, ReceivedAt: receivedAt,
		CaptureMethod: f.CaptureMethod, Valid: t.valid, Exclusion: exclusion,
		RawState: "disabled", Duplicate: f.Duplicate, Hold: f.Hold,
		Status: f.Status, TextStatus: f.TextStatus, TextError: f.TextError,
		Fighting: f.Fighting, Engine: f.Engine, Scene: f.Scene, LayoutProfile: f.LayoutProfile,
		LeftNinja: f.LeftNinja, RightNinja: f.RightNinja,
		LeftNinjaCandidate: f.LeftNinjaCandidate, RightNinjaCandidate: f.RightNinjaCandidate,
		LeftSlots: f.LeftSlots, RightSlots: f.RightSlots,
		PlayerSide: f.PlayerSide, PlayerName: f.PlayerName, OpponentName: f.OppName,
		Beads: append([]frame.Bead(nil), f.Beads...)}
	if f.Err != nil {
		o.Error = f.Err.Error()
	}
	w := work{frameID: id}
	if usableImage(f.Img) {
		bounds := f.Img.Bounds()
		o.Width, o.Height, o.OriginX, o.OriginY = bounds.Dx(), bounds.Dy(), bounds.Min.X, bounds.Min.Y
		if r.opts.RecordFrames {
			bytes := int64(bounds.Dx()) * int64(bounds.Dy()) * 4
			switch {
			case rawRecordingSkip(f) != "":
				o.RawState = rawRecordingSkip(f)
				r.status.SkippedFrames++
			case len(r.frameQueue) == cap(r.frameQueue):
				o.RawState = "dropped_image_queue_full"
				r.status.DroppedFrames++
			case bytes > r.rawQueueLimit()-r.status.QueueBytes:
				o.RawState = "dropped_queue_byte_limit"
				r.status.DroppedFrames++
			case r.recordingFull || r.status.RecordingBytes-r.status.HUDBytes >= r.rawDiskLimit():
				o.RawState = "dropped_recording_limit"
				r.status.DroppedFrames++
			default:
				w.img = image.NewRGBA(bounds)
				for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
					src, dst := f.Img.PixOffset(bounds.Min.X, y), w.img.PixOffset(bounds.Min.X, y)
					copy(w.img.Pix[dst:dst+bounds.Dx()*4], f.Img.Pix[src:src+bounds.Dx()*4])
				}
				w.bytes = bytes
				r.status.QueueBytes += bytes
				o.RawState = "pending"
			}
		}
	} else {
		o.RawState = "no_image"
	}
	r.prepareHUDLocked(f, receivedAt, &o, &w)
	w.entry = o
	if !r.enqueueLocked(work{entry: o, frameID: id}) {
		r.status.QueueBytes -= w.bytes + w.hudBytes
		if w.img != nil {
			r.status.DroppedFrames++
		}
		if w.hud != nil {
			r.status.DroppedHUD++
		}
		return id
	}
	if r.opts.RecordFrames {
		// The image worker owns replay ordering, including explicit skipped rows.
		// Its slow encoder cannot stall the separate capture/UI/event log writer.
		select {
		case r.frameQueue <- w:
		default:
			r.status.QueueBytes -= w.bytes + w.hudBytes
			if w.img != nil {
				r.status.DroppedFrames++
			}
			if w.hud != nil {
				r.status.DroppedHUD++
			}
			r.status.DroppedLogs++ // missing replay row, explicit frame index gap
			r.status.LastError = "image writer queue full; capture metadata retained, replay has a frame-index gap"
		}
	}
	return id
}

// Use this frame's positive scene evidence, never a remembered fight state.
// This gate runs before allocating/copying pixels or enqueueing PNG work.
func rawRecordingSkip(f frame.Frame) string {
	if f.Err != nil {
		return "skipped_capture_error"
	}
	if f.Hold {
		return "skipped_uncertain_scene"
	}
	if !f.Fighting || f.Scene != "fight" {
		return "skipped_not_fighting"
	}
	return ""
}

func invalidFrame(f frame.Frame, received time.Time) string {
	if !usableImage(f.Img) {
		return "no_valid_image"
	}
	if f.Err != nil {
		return "capture_or_analysis_error"
	}
	if f.Hold {
		return "held_frame"
	}
	if f.Duplicate {
		return "duplicate_frame"
	}
	if !ordered(f.CaptureStarted, f.CapturedAt, f.AnalysisStarted, f.AnalyzedAt, received) {
		return "missing_or_invalid_stage_time"
	}
	return ""
}

func usableImage(img *image.RGBA) bool {
	return img != nil && img.Bounds().Dx() > 0 && img.Bounds().Dy() > 0
}

func ordered(times ...time.Time) bool {
	for i, t := range times {
		if t.IsZero() || (i > 0 && t.Before(times[i-1])) {
			return false
		}
	}
	return true
}

func (r *Recorder) addHistoryLocked(t *trace) {
	if len(r.ring) < maxHistory {
		r.ring = append(r.ring, t.id)
	} else {
		delete(r.history, r.ring[r.ringNext])
		r.ring[r.ringNext] = t.id
		r.ringNext = (r.ringNext + 1) % maxHistory
	}
	r.history[t.id] = t
}

func (r *Recorder) enqueueLocked(w work) bool {
	select {
	case r.queue <- w:
		return true
	default:
		r.status.DroppedLogs++
		r.status.LastError = "diagnostic writer queue full; see dropped counters in report"
		return false
	}
}

func (r *Recorder) Snapshot() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

func (r *Recorder) run() {
	defer close(r.done)
	for w := range r.queue {
		r.writeEntry(w.entry)
	}
	<-r.framesDone
	if err := r.log.Close(); err != nil {
		r.setError(err)
	}
	if r.framesLog != nil {
		if err := r.framesLog.Close(); err != nil {
			r.setError(err)
		}
	}
}

func (r *Recorder) runFrames() {
	defer close(r.framesDone)
	for w := range r.frameQueue {
		if w.hud != nil {
			r.writeHUD(w)
		}
		if w.img != nil {
			r.writeFrame(w)
		} else if o, ok := w.entry.(observation); ok {
			r.writeReplay(o, "", o.RawState)
		}
	}
}

func (r *Recorder) writeEntry(entry any) {
	r.writeJSON(r.log, entry)
}

func (r *Recorder) writeJSON(file *os.File, entry any) {
	b, err := json.Marshal(entry)
	if err != nil {
		r.dropLog(err)
		return
	}
	b = append(b, '\n')
	r.logMu.Lock()
	defer r.logMu.Unlock()
	if int64(len(b)) > maxLogBytes-r.logBytes {
		r.dropLog(errors.New("capture log byte limit reached"))
		return
	}
	n, err := file.Write(b)
	r.logBytes += int64(n)
	if err != nil {
		r.dropLog(err)
	}
}

func (r *Recorder) dropLog(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.DroppedLogs++
	r.status.LastError = err.Error()
}

func (r *Recorder) setError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.LastError = err.Error()
	r.closeErr = err
}

var errRecordingLimit = errors.New("recording byte limit reached")

type limitedWriter struct {
	w         io.Writer
	remaining int64
	written   int64
}

func (w *limitedWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.remaining {
		return 0, errRecordingLimit
	}
	n, err := w.w.Write(b)
	w.remaining -= int64(n)
	w.written += int64(n)
	return n, err
}

func (r *Recorder) writeFrame(w work) {
	r.mu.Lock()
	remaining := r.rawDiskLimit() - (r.status.RecordingBytes - r.status.HUDBytes)
	dir := r.status.Directory
	r.mu.Unlock()
	rel := filepath.Join("frames", fmt.Sprintf("%09d.png", w.frameID))
	path := filepath.Join(dir, rel)
	tmp := path + ".partial"
	var count int64
	var err error
	if remaining <= 0 {
		err = errRecordingLimit
	} else {
		var file *os.File
		file, err = os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			writer := &limitedWriter{w: file, remaining: remaining}
			err = r.encodePNG(writer, w.img)
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
			count = writer.written
			if err == nil {
				err = os.Rename(tmp, path)
			}
		}
	}
	entry := map[string]any{"type": "raw_frame", "frame_id": w.frameID, "at": time.Now()}
	r.mu.Lock()
	r.status.QueueBytes -= w.bytes
	if err != nil {
		r.status.DroppedFrames++
		r.status.LastError = err.Error()
		entry["state"], entry["error"] = "dropped_write_error", err.Error()
		if errors.Is(err, errRecordingLimit) {
			entry["state"] = "dropped_recording_limit"
			r.recordingFull = true
		}
	} else {
		r.status.RecordedFrames++
		r.status.RecordingBytes += count
		entry["state"], entry["path"], entry["bytes"] = "saved", filepath.ToSlash(rel), count
	}
	r.mu.Unlock()
	if err != nil {
		_ = os.Remove(tmp)
	}
	r.writeEntry(entry)
	if err == nil {
		r.writeReplay(w.entry.(observation), filepath.ToSlash(rel), "saved")
	} else {
		r.writeReplay(w.entry.(observation), "", fmt.Sprint(entry["state"]))
	}
}

func (r *Recorder) writeReplay(o observation, path, state string) {
	if r.framesLog == nil {
		return
	}
	at := o.CapturedAt
	if at.IsZero() {
		at = o.ReceivedAt
	}
	offset := float64(at.Sub(r.started)) / float64(time.Millisecond)
	if offset < 0 {
		offset = 0
	}
	errText := o.Error
	if path == "" && errText == "" {
		errText = "raw frame unavailable: " + state
	}
	r.writeJSON(r.framesLog, map[string]any{
		"index": o.FrameID, "image": path, "rawState": state,
		"offsetMs": offset, "captureStarted": o.CaptureStarted, "capturedAt": o.CapturedAt,
		"duplicate": o.Duplicate, "error": errText, "width": o.Width, "height": o.Height,
	})
}

// Close stops intake, drains accepted writes, then produces the final report.
func (r *Recorder) Close() error {
	r.mu.Lock()
	if !r.status.Closed {
		r.status.Closed = true
		close(r.queue)
		close(r.frameQueue)
	}
	r.mu.Unlock()
	<-r.done
	r.mu.Lock()
	if r.ended.IsZero() {
		r.ended = time.Now()
	}
	r.mu.Unlock()
	_, err := r.ExportReport()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.Finalized = true
	if err != nil {
		return err
	}
	return r.closeErr
}
