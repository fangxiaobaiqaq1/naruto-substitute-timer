package diagnostics

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"time"

	"narutotimer/internal/frame"
)

const hudInterval = 500 * time.Millisecond

func (r *Recorder) hudDiskLimit() int64 {
	if r.opts.RecordHUDEvidence {
		return r.opts.MaxRecordingBytes / 4
	}
	return 0
}

func (r *Recorder) rawDiskLimit() int64 {
	return r.opts.MaxRecordingBytes - r.hudDiskLimit()
}

func (r *Recorder) rawQueueLimit() int64 {
	if r.opts.RecordHUDEvidence {
		return r.opts.MaxQueuedBytes - r.opts.MaxQueuedBytes/8
	}
	return r.opts.MaxQueuedBytes
}

// Retain lettering, bean bodies and their surroundings at the original scale.
// The coordinates come from this frame, not a cached layout or remembered fight.
// A strip is visual evidence only: its missing scene cannot drive replay gates.
func hudRegion(f frame.Frame) image.Rectangle {
	if !usableImage(f.Img) {
		return image.Rectangle{}
	}
	b := f.Img.Bounds()
	y0, y1 := b.Max.Y, b.Min.Y
	for _, bead := range f.Beads {
		if !image.Pt(bead.X, bead.Y).In(b) {
			continue
		}
		y0, y1 = min(y0, bead.Y), max(y1, bead.Y+1)
	}
	if y0 >= y1 {
		return image.Rectangle{}
	}
	region := image.Rect(b.Min.X, y0-max(1, b.Dx()*80/960), b.Max.X, y1+max(1, b.Dx()*20/960)).Intersect(b)
	if region.Dy() > b.Dy()/3 {
		return image.Rectangle{}
	}
	return region
}

func (r *Recorder) prepareHUDLocked(f frame.Frame, at time.Time, o *observation, w *work) {
	if !r.opts.RecordHUDEvidence {
		return
	}
	if reason := rawRecordingSkip(f); reason != "" {
		o.HUDState = reason
		return
	}
	if !usableImage(f.Img) {
		o.HUDState = "no_image"
		return
	}
	if !r.lastHUDAt.IsZero() && at.Sub(r.lastHUDAt) < hudInterval {
		o.HUDState = "sampling_interval"
		return
	}
	region := hudRegion(f)
	if region.Empty() {
		o.HUDState = "no_hud_region"
		return
	}
	r.lastHUDAt = at
	bytes := int64(region.Dx()) * int64(region.Dy()) * 4
	switch {
	case r.hudFull || r.status.HUDBytes >= r.hudDiskLimit():
		o.HUDState = "dropped_hud_limit"
	case len(r.frameQueue) == cap(r.frameQueue):
		o.HUDState = "dropped_image_queue_full"
	case bytes > r.opts.MaxQueuedBytes-r.status.QueueBytes:
		o.HUDState = "dropped_queue_byte_limit"
	default:
		w.hud = image.NewRGBA(region)
		for y := region.Min.Y; y < region.Max.Y; y++ {
			src, dst := f.Img.PixOffset(region.Min.X, y), w.hud.PixOffset(region.Min.X, y)
			copy(w.hud.Pix[dst:dst+region.Dx()*4], f.Img.Pix[src:src+region.Dx()*4])
		}
		w.hudBytes = bytes
		r.status.QueueBytes += bytes
		o.HUDState = "pending"
		return
	}
	r.status.DroppedHUD++
}

func (r *Recorder) writeHUD(w work) {
	r.mu.Lock()
	remaining := r.hudDiskLimit() - r.status.HUDBytes
	dir := r.status.Directory
	r.mu.Unlock()
	rel := filepath.Join("hud", fmt.Sprintf("%09d.png", w.frameID))
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
			err = r.encodePNG(writer, w.hud)
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
	b := w.hud.Bounds()
	o := w.entry.(observation)
	entry := map[string]any{"type": "hud_frame", "frame_id": w.frameID,
		"captured_at": o.CapturedAt, "crop": b, "source_width": o.Width, "source_height": o.Height,
		"note": "native pixels cropped without overlays; not a full frame or replay input"}
	r.mu.Lock()
	r.status.QueueBytes -= w.hudBytes
	if err != nil {
		r.status.DroppedHUD++
		r.status.LastError = "HUD evidence: " + err.Error()
		entry["state"], entry["error"] = "dropped_write_error", err.Error()
		if errors.Is(err, errRecordingLimit) {
			entry["state"] = "dropped_hud_limit"
			r.hudFull = true
		}
	} else {
		r.status.HUDFrames++
		r.status.HUDBytes += count
		r.status.RecordingBytes += count
		entry["state"], entry["path"], entry["bytes"] = "saved", filepath.ToSlash(rel), count
	}
	r.mu.Unlock()
	if err != nil {
		_ = os.Remove(tmp)
	}
	r.writeEntry(entry)
}
