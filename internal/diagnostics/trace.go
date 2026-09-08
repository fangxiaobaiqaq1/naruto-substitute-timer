package diagnostics

import (
	"fmt"
	"time"
)

// Confirm logs all confirmed decisions, including decisions hidden by the
// selected player side. Only visible decisions with correlated evidence qualify.
func (r *Recorder) Confirm(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.Closed {
		return
	}
	key := eventKey(e.Side, e.Serial)
	if r.eventKeys[key] {
		return
	}
	if len(r.eventKeys) >= r.opts.MaxObservations {
		r.exclusions["event_limit"]++
		return
	}
	r.eventKeys[key] = true
	r.events++
	et := &eventTrace{event: e}
	current := r.history[e.FrameID]
	var first *trace
	if !e.FirstObservedAt.IsZero() {
		for _, candidate := range r.history {
			if candidate.captured.Equal(e.FirstObservedAt) && candidate.valid {
				if first == nil || candidate.id < first.id {
					first = candidate
				}
			}
		}
	}
	if first != nil {
		et.firstFrameID, et.firstCapture, et.firstCaptured = first.id, first.captureStarted, first.captured
	}
	evidenceValid := current != nil && current.valid && first != nil && first.id <= e.FrameID &&
		ordered(first.captureStarted, first.captured, e.ConfirmedAt) && !e.ConfirmedAt.Before(current.analyzed)
	if evidenceValid {
		r.sampleLocked("event_confirmation_ms", first.captured, e.ConfirmedAt)
	}
	switch {
	case !e.UIVisible:
		et.exclusion = "event_not_visible"
	case current == nil || !current.valid:
		et.exclusion = "event_invalid_confirmation_frame"
	case first == nil:
		et.exclusion = "event_first_evidence_unavailable"
	case first.id > e.FrameID || !ordered(first.captureStarted, first.captured, e.ConfirmedAt) || e.ConfirmedAt.Before(current.analyzed):
		et.exclusion = "event_invalid_time_chain"
	default:
		et.valid = true
	}
	if et.exclusion != "" {
		r.exclusions[et.exclusion]++
	}
	if current != nil && current.drawn.IsZero() {
		current.events = append(current.events, et)
	}
	r.eventTraces[key] = et
	r.enqueueLocked(work{entry: map[string]any{"type": "event_confirmed", "event": e,
		"first_frame_id": et.firstFrameID, "valid_for_latency": et.valid, "exclusion": et.exclusion}})
	if current != nil {
		r.evaluateLocked(current)
	}
}

func eventKey(side string, serial uint64) string { return fmt.Sprintf("%s/%d", side, serial) }

// CarryEvents associates the decision actually shown by a UI snapshot with its
// frame. A superseding frame can display an earlier decision without claiming
// that the earlier frame itself was painted. Zero serial means not displayed.
func (r *Recorder) CarryEvents(frameID, leftSerial, rightSerial uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.Closed {
		return
	}
	t := r.history[frameID]
	if t == nil || !t.drawn.IsZero() {
		return
	}
	// This is the complete visible event selection of the applied UI snapshot.
	// Replace defaults so a side change before paint cannot retain a hidden event.
	t.events = nil
	for side, serial := range map[string]uint64{"left": leftSerial, "right": rightSerial} {
		if serial == 0 {
			continue
		}
		e := r.eventTraces[eventKey(side, serial)]
		if e == nil || !e.valid || e.complete {
			continue
		}
		found := false
		for _, existing := range t.events {
			if e == existing {
				found = true
				break
			}
		}
		if !found {
			t.events = append(t.events, e)
			r.enqueueLocked(work{entry: map[string]any{"type": "event_carried", "frame_id": frameID,
				"confirmation_frame_id": e.event.FrameID, "side": side, "serial": serial}})
		}
	}
	r.evaluateLocked(t)
}

// MarkUI accepts actual UI milestones. In particular drawn must only be sent
// after painting and buffer swap, never from Refresh or the UI-post callback.
func (r *Recorder) MarkUI(frameIDs []uint64, stage string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.Closed {
		return
	}
	for _, id := range frameIDs {
		t := r.history[id]
		if t == nil {
			r.exclusions["ui_unknown_or_evicted_frame"]++
			continue
		}
		var target *time.Time
		switch stage {
		case "queued":
			target = &t.queued
		case "applied":
			target = &t.applied
		case "drawing":
			target = &t.drawing
		case "drawn":
			target = &t.drawn
		default:
			r.exclusions["ui_unknown_stage"]++
			continue
		}
		if !target.IsZero() {
			continue
		} // Repaints are not new latency samples.
		if at.IsZero() {
			r.exclusions["ui_missing_stage_time"]++
			continue
		}
		*target = at
		r.enqueueLocked(work{entry: map[string]any{"type": "ui_stage", "frame_id": id, "stage": stage, "at": at}})
		r.evaluateLocked(t)
	}
}

func (r *Recorder) sampleLocked(name string, start, end time.Time) {
	if !ordered(start, end) {
		return
	}
	if len(r.samples[name]) >= r.opts.MaxObservations {
		return
	}
	r.samples[name] = append(r.samples[name], float64(end.Sub(start))/float64(time.Millisecond))
}

func (r *Recorder) evaluateLocked(t *trace) {
	if !t.valid {
		return
	}
	stages := []struct {
		name       string
		start, end time.Time
	}{
		{"received_to_ui_queue_ms", t.received, t.queued},
		{"ui_dispatch_ms", t.queued, t.applied},
		{"ui_paint_wait_ms", t.applied, t.drawing},
		{"ui_paint_submit_ms", t.drawing, t.drawn},
	}
	for _, s := range stages {
		if !t.counted[s.name] && ordered(s.start, s.end) {
			r.sampleLocked(s.name, s.start, s.end)
			t.counted[s.name] = true
		}
	}
	complete := ordered(t.captureStarted, t.captured, t.analysisStarted, t.analyzed,
		t.received, t.queued, t.applied, t.drawing, t.drawn)
	if complete && !t.complete {
		t.complete = true
		r.status.CompleteFrames++
		r.sampleLocked("frame_end_to_end_ms", t.captureStarted, t.drawn)
		r.enqueueLocked(work{entry: map[string]any{"type": "frame_latency", "frame_id": t.id,
			"capture_request_to_draw_submit_ms": float64(t.drawn.Sub(t.captureStarted)) / float64(time.Millisecond)}})
	}
	if !complete {
		return
	}
	for _, e := range t.events {
		if !e.valid || e.complete {
			continue
		}
		// The event must have been confirmed before this UI snapshot was queued.
		if !ordered(e.firstCapture, e.firstCaptured, e.event.ConfirmedAt, t.queued, t.applied, t.drawing, t.drawn) {
			continue
		}
		e.complete = true
		r.status.CompleteEvents++
		r.sampleLocked("event_end_to_end_ms", e.firstCapture, t.drawn)
		r.sampleLocked("event_confirmed_to_ui_queue_ms", e.event.ConfirmedAt, t.queued)
		r.sampleLocked("event_confirmed_to_draw_ms", e.event.ConfirmedAt, t.drawn)
		r.enqueueLocked(work{entry: map[string]any{"type": "event_latency", "event": e.event,
			"first_frame_id": e.firstFrameID, "display_frame_id": t.id,
			"first_capture_request_to_draw_submit_ms": float64(t.drawn.Sub(e.firstCapture)) / float64(time.Millisecond)}})
	}
}
