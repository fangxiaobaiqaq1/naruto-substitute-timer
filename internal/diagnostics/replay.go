package diagnostics

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	imagedraw "image/draw"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"narutotimer/internal/frame"
)

const (
	defaultReplayDuration = 180 * time.Second
	defaultReplayInterval = time.Second
	defaultReplayBytes    = int64(64 << 20)
	// Four full-resolution deep copies are enough to absorb short JPEG/disk
	// stalls without turning a sampled replay into a large raw-pixel buffer.
	defaultReplayQueue = 4
	replayMaxWidth     = 720
	replayJPEGQuality  = 75
)

type replayState struct {
	Scene               string       `json:"scene"`
	Fighting            bool         `json:"fighting"`
	Hold                bool         `json:"hold"`
	Duplicate           bool         `json:"duplicate"`
	Error               string       `json:"error,omitempty"`
	Status              string       `json:"status,omitempty"`
	TextStatus          string       `json:"text_status,omitempty"`
	TextError           string       `json:"text_error,omitempty"`
	Engine              string       `json:"engine,omitempty"`
	LayoutProfile       string       `json:"layout_profile,omitempty"`
	LeftNinja           string       `json:"left_ninja,omitempty"`
	RightNinja          string       `json:"right_ninja,omitempty"`
	LeftNinjaCandidate  string       `json:"left_ninja_candidate,omitempty"`
	RightNinjaCandidate string       `json:"right_ninja_candidate,omitempty"`
	LeftSlots           int          `json:"left_slots"`
	RightSlots          int          `json:"right_slots"`
	PlayerSide          string       `json:"player_side,omitempty"`
	PlayerName          string       `json:"player_name,omitempty"`
	OpponentName        string       `json:"opponent_name,omitempty"`
	Beads               []frame.Bead `json:"beads,omitempty"`
	// Frame has no timer-event counter. This field makes absence explicit rather
	// than inventing UI state that cannot be correlated to this source image.
	EventState string `json:"event_state"`
}

// ReplayUIState is a serializable snapshot of the timer text prepared for one
// capture frame. The UI owns this value; the replay worker only receives this
// plain data and never reads Fyne objects.
type ReplayUIState struct {
	FrameID       uint64 `json:"frame_id"`
	PlayerSide    string `json:"player_side,omitempty"`
	OpponentSide  string `json:"opponent_side,omitempty"`
	OpponentNinja string `json:"opponent_ninja,omitempty"`
	// PrimaryText, AlternateText, and EventText are the values prepared when
	// the capture frame was handled. Applied* records a later Fyne callback's
	// wall-clock recomputation when queue delay changed presentation.
	PrimaryText          string    `json:"primary_text,omitempty"`
	AlternateText        string    `json:"alternate_text,omitempty"`
	EventText            string    `json:"event_text,omitempty"`
	AppliedPrimaryText   string    `json:"applied_primary_text,omitempty"`
	AppliedAlternateText string    `json:"applied_alternate_text,omitempty"`
	AppliedEventText     string    `json:"applied_event_text,omitempty"`
	LeftEventCount       uint64    `json:"left_event_count"`
	RightEventCount      uint64    `json:"right_event_count"`
	PreparedAt           time.Time `json:"prepared_at,omitempty"`
	AppliedAt            time.Time `json:"applied_at,omitempty"`
	DrawnAt              time.Time `json:"drawn_at,omitempty"`
	Unavailable          string    `json:"unavailable,omitempty"`
}

type replayWork struct {
	img                       *image.RGBA
	frameID                   uint64
	capturedAt                time.Time
	captureMethod             string
	sourceWidth, sourceHeight int
	state                     replayState
}

type replayTimerEvent struct {
	Side                string    `json:"side"`
	Serial              uint64    `json:"serial"`
	Number              uint64    `json:"number"`
	FirstFrameID        uint64    `json:"first_frame_id,omitempty"`
	ConfirmationFrameID uint64    `json:"confirmation_frame_id"`
	DisplayFrameID      uint64    `json:"display_frame_id,omitempty"`
	FirstObservedAt     time.Time `json:"first_observed_at"`
	ConfirmedAt         time.Time `json:"confirmed_at"`
	UIVisible           bool      `json:"ui_visible"`
}

type replayFrame struct {
	FrameID            uint64    `json:"frame_id"`
	CapturedAt         time.Time `json:"captured_at"`
	OffsetMS           float64   `json:"offset_ms"`
	OffsetBeforeStopMS *float64  `json:"offset_before_stop_ms,omitempty"`
	// Path remains the raw source image path for schema-v1 readers.
	Path            string             `json:"path,omitempty"`
	RawPath         string             `json:"raw_path,omitempty"`
	AnnotatedPath   string             `json:"annotated_path,omitempty"`
	SourceWidth     int                `json:"source_width"`
	SourceHeight    int                `json:"source_height"`
	OutputWidth     int                `json:"output_width,omitempty"`
	OutputHeight    int                `json:"output_height,omitempty"`
	OutputBytes     int64              `json:"output_bytes,omitempty"`
	AnnotatedWidth  int                `json:"annotated_width,omitempty"`
	AnnotatedHeight int                `json:"annotated_height,omitempty"`
	AnnotatedBytes  int64              `json:"annotated_bytes,omitempty"`
	State           string             `json:"state"`
	CaptureMethod   string             `json:"capture_method,omitempty"`
	Error           string             `json:"error,omitempty"`
	StateEvidence   replayState        `json:"state_evidence"`
	UIState         ReplayUIState      `json:"ui_state"`
	TimerEvents     []replayTimerEvent `json:"timer_events,omitempty"`
}

type replayManifest struct {
	SchemaVersion     int           `json:"schema_version"`
	RequestedDuration string        `json:"requested_duration"`
	Cutoff            time.Time     `json:"cutoff"`
	ActualStart       *time.Time    `json:"actual_start,omitempty"`
	ActualEnd         *time.Time    `json:"actual_end,omitempty"`
	PartialHistory    bool          `json:"partial_history"`
	Sampling          string        `json:"sampling_policy"`
	MaxWidth          int           `json:"max_width"`
	Format            string        `json:"format"`
	Quality           int           `json:"quality"`
	ByteCap           int64         `json:"byte_cap"`
	Saved             uint64        `json:"saved"`
	Dropped           uint64        `json:"dropped"`
	Evicted           uint64        `json:"evicted"`
	Frames            []replayFrame `json:"frames"`
}

// replayRecorder is deliberately independent of the PNG worker. It owns only
// sampled JPEGs on disk; it never retains a multi-minute raw-pixel ring.
type replayRecorder struct {
	parent     *Recorder
	dir        string
	sessionDir string
	duration   time.Duration
	interval   time.Duration
	capBytes   int64
	queue      chan replayWork
	done       chan struct{}

	mu           sync.Mutex // protects sampling state, stop cutoff and queue closure
	lastSampleAt time.Time
	stopped      bool
	cutoff       time.Time

	// worker-owned after construction
	frames []replayFrame
	bytes  int64
	ui     map[uint64]ReplayUIState
}

func newReplayRecorder(r *Recorder, dir string) *replayRecorder {
	return &replayRecorder{parent: r, dir: dir, sessionDir: r.status.Directory, duration: r.opts.ReplayDuration,
		interval: r.opts.ReplayInterval, capBytes: r.opts.MaxReplayBytes,
		queue: make(chan replayWork, defaultReplayQueue), done: make(chan struct{}), ui: make(map[uint64]ReplayUIState)}
}

// RecordReplayUIState is called by the UI only after it has computed the
// display for id. It cannot retrofit a later display onto an older frame.
func (r *Recorder) RecordReplayUIState(state ReplayUIState) {
	if state.FrameID == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.replay == nil || r.status.Closed {
		return
	}
	r.replay.mu.Lock()
	defer r.replay.mu.Unlock()
	if _, exists := r.replay.ui[state.FrameID]; !exists {
		r.replay.ui[state.FrameID] = state
	}
}

// RecordReplayUIApplied records the text actually assigned by the surviving
// UI callback. It updates only presentation metadata already associated with a
// frame; it never reads or stores source pixels.
func (r *Recorder) RecordReplayUIApplied(frameID uint64, primary, alternate, event string) {
	if frameID == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.replay == nil || r.status.Closed {
		return
	}
	r.replay.mu.Lock()
	defer r.replay.mu.Unlock()
	state, ok := r.replay.ui[frameID]
	if !ok {
		return
	}
	state.AppliedPrimaryText = primary
	state.AppliedAlternateText = alternate
	state.AppliedEventText = event
	r.replay.ui[frameID] = state
}

func (r *Recorder) MarkReplayUIDrawn(frameID uint64, applied, drawn time.Time) {
	if frameID == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.replay == nil {
		return
	}
	r.replay.mu.Lock()
	defer r.replay.mu.Unlock()
	state, ok := r.replay.ui[frameID]
	if !ok {
		return
	}
	if state.AppliedAt.IsZero() {
		state.AppliedAt = applied
	}
	if state.DrawnAt.IsZero() {
		state.DrawnAt = drawn
	}
	r.replay.ui[frameID] = state
}

// enqueueLocked is called under Recorder.mu. Sampling and queue admission are
// decided before the copy, then the copied pixels are handed to a non-blocking
// dedicated worker. Invalid/no-image frames intentionally do not enter replay.
func (q *replayRecorder) enqueueLocked(f replayWork) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped || f.img == nil {
		return
	}
	at := f.capturedAt
	if at.IsZero() {
		at = time.Now()
		f.capturedAt = at
	}
	if !q.lastSampleAt.IsZero() && !at.Before(q.lastSampleAt) && at.Sub(q.lastSampleAt) < q.interval {
		return
	}
	// Do not pay a full-image copy if bounded backpressure has already rejected
	// the sample. The worker is the sole receiver, so this check is advisory;
	// the non-blocking send below remains the admission authority.
	if len(q.queue) == cap(q.queue) {
		q.parent.status.ReplayDropped++
		return
	}
	// Out-of-order timestamps are still safe to preserve; they must not reset
	// the cadence anchor and cause a burst of copies.
	if at.After(q.lastSampleAt) {
		q.lastSampleAt = at
	}
	bounds := f.img.Bounds()
	copyImg := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		src := f.img.PixOffset(bounds.Min.X, y)
		dst := copyImg.PixOffset(bounds.Min.X, y)
		copy(copyImg.Pix[dst:dst+bounds.Dx()*4], f.img.Pix[src:src+bounds.Dx()*4])
	}
	f.img = copyImg
	select {
	case q.queue <- f:
	default:
		q.parent.status.ReplayDropped++
	}
}

func (q *replayRecorder) stop(cutoff time.Time) {
	q.mu.Lock()
	if !q.stopped {
		q.stopped = true
		q.cutoff = cutoff
		close(q.queue)
	}
	q.mu.Unlock()
}

func (q *replayRecorder) run() {
	defer close(q.done)
	for w := range q.queue {
		// The queue was closed under Recorder's admission lock, so every item
		// here was accepted before the frozen cutoff and must be drained.
		q.write(w)
	}
	q.prune(q.cutoff.Add(-q.duration))
	q.writeAnnotations()
	q.writeManifest()
}

func (q *replayRecorder) write(w replayWork) {
	img := scaleReplay(w.img)
	bounds := img.Bounds()
	rel := filepath.ToSlash(filepath.Join("replay", fmt.Sprintf("%09d.jpg", w.frameID)))
	path, tmp := filepath.Join(q.sessionDir, filepath.FromSlash(rel)), filepath.Join(q.sessionDir, filepath.FromSlash(rel))+".partial"
	entry := replayFrame{FrameID: w.frameID, CapturedAt: w.capturedAt, OffsetMS: float64(w.capturedAt.Sub(q.parent.started)) / float64(time.Millisecond), Path: rel, RawPath: rel, SourceWidth: w.sourceWidth, SourceHeight: w.sourceHeight, OutputWidth: bounds.Dx(), OutputHeight: bounds.Dy(), CaptureMethod: w.captureMethod, StateEvidence: w.state}
	if entry.OffsetMS < 0 {
		entry.OffsetMS = 0
	}
	size, err := writeReplayJPEG(tmp, img)
	if err != nil || size > q.capBytes/3 || !q.evictToFit(size) {
		_ = os.Remove(tmp)
		if err == nil {
			err = errors.New("raw replay JPEG exceeds reserved paired cap")
		}
		entry.Path, entry.RawPath, entry.State, entry.Error = "", "", "dropped_byte_cap", err.Error()
		q.frames = append(q.frames, entry)
		q.parent.replayDrop(err)
		return
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		entry.Path, entry.RawPath, entry.State, entry.Error = "", "", "dropped_write_error", err.Error()
		q.frames = append(q.frames, entry)
		q.parent.replayDrop(err)
		return
	}
	entry.State, entry.OutputBytes = "saved", size
	q.frames = append(q.frames, entry)
	q.bytes += size
	q.parent.replaySaved(size)
	q.prune(w.capturedAt.Add(-q.duration))
}

func writeReplayJPEG(path string, value image.Image) (int64, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return 0, err
	}
	err = jpeg.Encode(file, value, &jpeg.Options{Quality: replayJPEGQuality})
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		_ = os.Remove(path)
		return 0, err
	}
	return info.Size(), nil
}

// writeAnnotations runs only after the replay cutoff, when all accepted UI
// callbacks have had a chance to attach their exact frame-id snapshot.
func (q *replayRecorder) writeAnnotations() {
	for i := range q.frames {
		entry := &q.frames[i]
		if entry.State != "saved" {
			continue
		}
		state := ReplayUIState{FrameID: entry.FrameID, Unavailable: "not available on this frame"}
		q.mu.Lock()
		if saved, ok := q.ui[entry.FrameID]; ok {
			state = saved
		}
		q.mu.Unlock()
		entry.UIState = state
		file, err := os.Open(filepath.Join(q.sessionDir, filepath.FromSlash(entry.RawPath)))
		if err == nil {
			var decoded image.Image
			decoded, err = jpeg.Decode(file)
			_ = file.Close()
			if err == nil {
				annotated := annotateReplay(decoded, *entry)
				rel := filepath.ToSlash(filepath.Join("replay", "annotated", fmt.Sprintf("%09d.jpg", entry.FrameID)))
				path := filepath.Join(q.sessionDir, filepath.FromSlash(rel))
				_ = os.MkdirAll(filepath.Dir(path), 0755)
				size, writeErr := writeReplayJPEG(path+".partial", annotated)
				if writeErr == nil && q.bytes+size <= q.capBytes {
					writeErr = os.Rename(path+".partial", path)
				} else if writeErr == nil {
					writeErr = errors.New("annotated replay exceeds shared byte cap")
				}
				if writeErr == nil {
					entry.AnnotatedPath, entry.AnnotatedBytes, entry.AnnotatedWidth, entry.AnnotatedHeight = rel, size, annotated.Bounds().Dx(), annotated.Bounds().Dy()
					q.bytes += size
					q.parent.replayBytes(size)
					continue
				}
				err = writeErr
				_ = os.Remove(path + ".partial")
			}
		}
		_ = os.Remove(filepath.Join(q.sessionDir, filepath.FromSlash(entry.RawPath)))
		q.bytes -= entry.OutputBytes
		q.parent.replayEvicted(entry.OutputBytes)
		entry.Path, entry.RawPath, entry.State = "", "", "dropped_annotation_error"
		entry.Error = err.Error()
		q.parent.replayDrop(err)
	}
}

func replayText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		} else {
			b.WriteString("?")
		}
	}
	return b.String()
}

func replayLines(e replayFrame) []string {
	u := e.UIState
	left, right := "unknown", "unknown"
	if u.LeftEventCount > 0 {
		left = fmt.Sprint(u.LeftEventCount)
	}
	if u.RightEventCount > 0 {
		right = fmt.Sprint(u.RightEventCount)
	}
	lines := []string{
		fmt.Sprintf("frame_id=%d captured_at=%s offset=%.0fms", e.FrameID, e.CapturedAt.Format(time.RFC3339Nano), e.OffsetMS),
		fmt.Sprintf("left ninja=%s candidate=%s", replayText(e.StateEvidence.LeftNinja), replayText(e.StateEvidence.LeftNinjaCandidate)),
		fmt.Sprintf("right ninja=%s candidate=%s", replayText(e.StateEvidence.RightNinja), replayText(e.StateEvidence.RightNinjaCandidate)),
		fmt.Sprintf("player=%s opponent=%s (%s)", replayText(e.StateEvidence.PlayerSide), replayText(u.OpponentSide), replayText(u.OpponentNinja)),
		fmt.Sprintf("slots L=%d R=%d beads=%s", e.StateEvidence.LeftSlots, e.StateEvidence.RightSlots, replayBeads(e.StateEvidence.Beads)),
		fmt.Sprintf("timer prepared=%s alt=%s event=%s counts L=%s R=%s", replayText(u.PrimaryText), replayText(u.AlternateText), replayText(u.EventText), left, right),
		fmt.Sprintf("timer applied=%s alt=%s event=%s", replayText(u.AppliedPrimaryText), replayText(u.AppliedAlternateText), replayText(u.AppliedEventText)),
		fmt.Sprintf("capture scene=%s fighting=%v hold=%v status=%s", replayText(e.StateEvidence.Scene), e.StateEvidence.Fighting, e.StateEvidence.Hold, replayText(e.StateEvidence.Status)),
		fmt.Sprintf("ui prepared=%s applied=%s drawn=%s", stamp(u.PreparedAt), stamp(u.AppliedAt), stamp(u.DrawnAt)),
		"timer evidence=" + replayText(u.Unavailable),
	}
	return lines
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "unavailable"
	}
	return t.Format(time.RFC3339Nano)
}
func replayBeads(beads []frame.Bead) string {
	if len(beads) == 0 {
		return "unknown"
	}
	parts := make([]string, len(beads))
	for i, b := range beads {
		state := "dark"
		if b.Unknown {
			state = "unknown"
		} else if b.Gold {
			state = "gold"
		} else if b.Lit {
			state = "lit"
		}
		parts[i] = b.Label + ":" + state
	}
	return strings.Join(parts, ",")
}

func annotateReplay(src image.Image, e replayFrame) *image.RGBA {
	const footer = 180
	b := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()+footer))
	imagedraw.Draw(out, image.Rect(0, 0, b.Dx(), b.Dy()), src, b.Min, imagedraw.Src)
	imagedraw.Draw(out, image.Rect(0, b.Dy(), b.Dx(), b.Dy()+footer), &image.Uniform{C: color.RGBA{R: 12, G: 18, B: 28, A: 255}}, image.Point{}, imagedraw.Src)
	for y, line := range replayLines(e) {
		drawReplayText(out, 6, b.Dy()+6+y*18, line)
	}
	return out
}

// A tiny deterministic bitmap keeps exported annotation readable without a
// platform font file or Fyne dependency. Non-ASCII input is escaped above.
func drawReplayText(dst *image.RGBA, x, y int, text string) {
	for _, r := range text {
		if r < 0x20 || r > 0x7e {
			r = '?'
		}
		glyph := basicGlyph(byte(r))
		for row, bits := range glyph {
			for col := 0; col < 5; col++ {
				if bits&(1<<uint(4-col)) != 0 {
					for dy := 0; dy < 2; dy++ {
						for dx := 0; dx < 2; dx++ {
							px, py := x+col*2+dx, y+row*2+dy
							if image.Pt(px, py).In(dst.Bounds()) {
								dst.SetRGBA(px, py, color.RGBA{R: 255, G: 255, B: 255, A: 255})
							}
						}
					}
				}
			}
		}
		x += 12
		if x >= dst.Bounds().Dx()-10 {
			return
		}
	}
}

func basicGlyph(c byte) [7]byte {
	if c >= 'a' && c <= 'z' {
		c -= 'a' - 'A'
	}
	if c >= 'A' && c <= 'Z' {
		return [26][7]byte{{14, 17, 17, 31, 17, 17, 17}, {30, 17, 17, 30, 17, 17, 30}, {15, 16, 16, 16, 16, 16, 15}, {30, 17, 17, 17, 17, 17, 30}, {31, 16, 16, 30, 16, 16, 31}, {31, 16, 16, 30, 16, 16, 16}, {15, 16, 16, 23, 17, 17, 15}, {17, 17, 17, 31, 17, 17, 17}, {31, 4, 4, 4, 4, 4, 31}, {1, 1, 1, 1, 17, 17, 14}, {17, 18, 20, 24, 20, 18, 17}, {16, 16, 16, 16, 16, 16, 31}, {17, 27, 21, 21, 17, 17, 17}, {17, 25, 21, 19, 17, 17, 17}, {14, 17, 17, 17, 17, 17, 14}, {30, 17, 17, 30, 16, 16, 16}, {14, 17, 17, 17, 21, 18, 13}, {30, 17, 17, 30, 20, 18, 17}, {15, 16, 16, 14, 1, 1, 30}, {31, 4, 4, 4, 4, 4, 4}, {17, 17, 17, 17, 17, 17, 14}, {17, 17, 17, 17, 17, 10, 4}, {17, 17, 17, 21, 21, 21, 10}, {17, 17, 10, 4, 10, 17, 17}, {17, 17, 10, 4, 4, 4, 4}, {31, 1, 2, 4, 8, 16, 31}}[c-'A']
	}
	if c >= '0' && c <= '9' {
		return [10][7]byte{{14, 17, 19, 21, 25, 17, 14}, {4, 12, 4, 4, 4, 4, 14}, {14, 17, 1, 2, 4, 8, 31}, {30, 1, 1, 14, 1, 1, 30}, {2, 6, 10, 18, 31, 2, 2}, {31, 16, 16, 30, 1, 1, 30}, {14, 16, 16, 30, 17, 17, 14}, {31, 1, 2, 4, 8, 8, 8}, {14, 17, 17, 14, 17, 17, 14}, {14, 17, 17, 15, 1, 1, 14}}[c-'0']
	}
	switch c {
	case '=':
		return [7]byte{0, 31, 0, 31}
	case ':':
		return [7]byte{0, 4, 0, 0, 4}
	case '.':
		return [7]byte{0, 0, 0, 0, 0, 6, 6}
	case '-':
		return [7]byte{0, 0, 0, 31}
	case '/':
		return [7]byte{1, 2, 4, 8, 16}
	case '_':
		return [7]byte{0, 0, 0, 0, 0, 0, 31}
	case ',':
		return [7]byte{0, 0, 0, 0, 6, 6, 4}
	case '(':
		return [7]byte{2, 4, 8, 8, 8, 4, 2}
	case ')':
		return [7]byte{8, 4, 2, 2, 2, 4, 8}
	case '?':
		return [7]byte{14, 17, 1, 2, 4, 0, 4}
	case ' ':
		return [7]byte{}
	}
	return [7]byte{31, 17, 21, 17, 21, 17, 31}
}

func scaleReplay(src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	width, height := b.Dx(), b.Dy()
	if width <= replayMaxWidth {
		return src
	}
	outW := replayMaxWidth
	outH := height * outW / width
	if outH < 1 {
		outH = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, outW, outH))
	for y := 0; y < outH; y++ {
		sy := b.Min.Y + y*height/outH
		for x := 0; x < outW; x++ {
			sx := b.Min.X + x*width/outW
			s := src.PixOffset(sx, sy)
			d := out.PixOffset(x, y)
			copy(out.Pix[d:d+4], src.Pix[s:s+4])
		}
	}
	return out
}

func (q *replayRecorder) evictToFit(incoming int64) bool {
	for q.bytes+incoming > q.capBytes {
		index := -1
		for i := range q.frames {
			if q.frames[i].State == "saved" {
				index = i
				break
			}
		}
		if index < 0 || !q.evict(index) {
			return false
		}
	}
	return true
}

func (q *replayRecorder) prune(before time.Time) {
	for i := range q.frames {
		if q.frames[i].State == "saved" && q.frames[i].CapturedAt.Before(before) {
			_ = q.evict(i)
		}
	}
}

func (q *replayRecorder) evict(index int) bool {
	entry := &q.frames[index]
	for _, rel := range []string{entry.RawPath, entry.AnnotatedPath} {
		if rel == "" {
			continue
		}
		if err := os.Remove(filepath.Join(q.sessionDir, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
			q.parent.replayDrop(err)
			return false
		}
	}
	bytes := entry.OutputBytes + entry.AnnotatedBytes
	q.bytes -= bytes
	entry.State = "evicted"
	q.parent.replayEvicted(bytes)
	return true
}

// replayTimerEventsLocked converts the recorder's authoritative event traces into
// lightweight frame references. Replay must not infer timer counters from pixels,
// but it can preserve the existing frame/event correlation maintained by trace.go.
func (r *Recorder) replayTimerEventsLocked() map[uint64][]replayTimerEvent {
	out := make(map[uint64][]replayTimerEvent)
	for _, et := range r.eventTraces {
		if et == nil {
			continue
		}
		e := replayTimerEvent{Side: et.event.Side, Serial: et.event.Serial, Number: et.event.Number,
			FirstFrameID: et.firstFrameID, ConfirmationFrameID: et.event.FrameID,
			FirstObservedAt: et.event.FirstObservedAt, ConfirmedAt: et.event.ConfirmedAt, UIVisible: et.event.UIVisible}
		out[et.event.FrameID] = append(out[et.event.FrameID], e)
		for _, tr := range r.history {
			for _, carried := range tr.events {
				if carried == et {
					e.DisplayFrameID = tr.id
					out[tr.id] = append(out[tr.id], e)
				}
			}
		}
	}
	return out
}

func (q *replayRecorder) writeManifest() {
	var start, end *time.Time
	for _, f := range q.frames {
		if f.State != "saved" {
			continue
		}
		at := f.CapturedAt
		if start == nil || at.Before(*start) {
			v := at
			start = &v
		}
		if end == nil || at.After(*end) {
			v := at
			end = &v
		}
	}
	q.parent.mu.Lock()
	status := q.parent.status
	timers := q.parent.replayTimerEventsLocked()
	q.parent.mu.Unlock()
	for i := range q.frames {
		q.frames[i].TimerEvents = append(q.frames[i].TimerEvents, timers[q.frames[i].FrameID]...)
	}
	partial := start == nil || q.cutoff.Sub(*start) < q.duration
	for i := range q.frames {
		if q.frames[i].CapturedAt.IsZero() || q.cutoff.IsZero() {
			continue
		}
		offset := float64(q.frames[i].CapturedAt.Sub(q.cutoff)) / float64(time.Millisecond)
		q.frames[i].OffsetBeforeStopMS = &offset
	}
	manifest := replayManifest{SchemaVersion: 2, RequestedDuration: q.duration.String(), Cutoff: q.cutoff,
		ActualStart: start, ActualEnd: end, PartialHistory: partial,
		Sampling: fmt.Sprintf("bounded diagnostic sampling: at most one source frame per %s; raw JPEG plus same-frame annotated JPEG share the %d-byte cap; replay worker never accesses UI objects", q.interval, q.capBytes),
		MaxWidth: replayMaxWidth, Format: "JPEG", Quality: replayJPEGQuality, ByteCap: q.capBytes,
		Saved: status.ReplaySaved, Dropped: status.ReplayDropped, Evicted: status.ReplayEvicted,
		Frames: q.frames}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		q.parent.replayDrop(err)
		return
	}
	if err = atomicWrite(filepath.Join(q.dir, "manifest.json"), data); err != nil {
		q.parent.replayDrop(err)
	}
}

// ExportReplay writes bounded raw and same-frame annotated replay JPEG pairs
// plus their aligned manifest. It deliberately never includes raw PNGs, HUD
// evidence, capture.jsonl, or the support bundle's configuration/inventory data.
func (r *Recorder) ExportReplay(root string) (string, error) {
	r.mu.Lock()
	if r.replay == nil {
		r.mu.Unlock()
		return "", errors.New("diagnostic replay was not enabled for this session")
	}
	dir := r.status.Directory
	bytes := r.opts.MaxReplayBytes
	closed := r.status.Closed
	r.mu.Unlock()
	if !closed {
		return "", errors.New("stop diagnostics before exporting the replay so its cutoff is final")
	}
	<-r.replay.done
	manifest := filepath.Join(dir, "replay", "manifest.json")
	if _, err := os.Stat(manifest); err != nil {
		return "", fmt.Errorf("replay is unavailable: %w", err)
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		return "", err
	}
	var m replayManifest
	if err = json.Unmarshal(data, &m); err != nil {
		return "", err
	}
	if m.Saved == 0 {
		return "", errors.New("replay contains no saved images")
	}
	if root == "" {
		root = filepath.Dir(dir)
	}
	outDir := filepath.Join(root, "replay-exports")
	if err = os.MkdirAll(outDir, 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(outDir, ".replay-*.zip")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	zw := zip.NewWriter(tmp)
	var total int64
	add := func(source, name string) error {
		info, err := os.Stat(source)
		if err != nil {
			return err
		}
		if total+info.Size() > bytes {
			return errors.New("replay export exceeds replay byte cap")
		}
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := zw.Create(filepath.ToSlash(name))
		if err != nil {
			return err
		}
		if _, err = io.Copy(out, in); err != nil {
			return err
		}
		total += info.Size()
		return nil
	}
	if err = add(manifest, "manifest.json"); err == nil {
		for _, entry := range m.Frames {
			if entry.State != "saved" {
				continue
			}
			for _, rel := range []string{entry.RawPath, entry.AnnotatedPath} {
				if rel == "" {
					err = errors.New("saved replay frame is missing a paired path")
					break
				}
				if err = add(filepath.Join(dir, filepath.FromSlash(rel)), rel); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	final := filepath.Join(outDir, "diagnostic-replay-"+time.Now().Format("20060102-150405.000")+".zip")
	if err = os.Rename(tmpPath, final); err != nil {
		return "", err
	}
	return final, nil
}
