package main

import (
	"bufio"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"testing"
	"time"

	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/ninja"
)

func TestNativeMixedSweepDoesNotInventEventsAndKeepsRealDrop(t *testing.T) {
	directory := "../../debug/mixed-bead-flicker-20260916-native"
	f, err := os.Open(filepath.Join(directory, "frames.jsonl"))
	if os.IsNotExist(err) {
		t.Skip("local native recording is not distributed")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg := integrationConfig(t)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		t.Fatal(err)
	}
	tracker := newReplayTracker(cfg, ninja.DefaultCooldown)
	var last frame.Frame
	count := 0
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var entry sample
		if err := json.Unmarshal(scan.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Error != "" {
			t.Fatal(entry.Error)
		}
		img := purpleEvidence(t, filepath.Join(directory, entry.Image))
		last = frame.AnalyzeImage(img, eng, factory.FromApp(cfg).Mode, entry.Started, entry.Captured, "native-sweep-recording")
		last.Duplicate = entry.Duplicate
		left, right := knownCount(last.Beads, 'L', last.LeftSlots), knownCount(last.Beads, 'R', last.RightSlots)
		if last.Err != nil || !last.Fighting || last.Hold || last.LayoutProfile != "camp" || left == nil || right == nil || *left != 4 || *right != 5 {
			t.Fatalf("%s: %+v", entry.Image, last)
		}
		if events := tracker.observe(last); len(events) != 0 {
			t.Fatalf("idle sweep fabricated event: %+v", events)
		}
		count++
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	if count < 2 {
		t.Fatal("empty/short recording")
	}
	// Explicit synthetic positive control after the real idle recording.
	// Both current-color drops must still be confirmed at the first fresh frame.
	dropped := image.NewRGBA(last.Img.Bounds())
	draw.Draw(dropped, dropped.Bounds(), last.Img, last.Img.Bounds().Min, draw.Src)
	for _, b := range last.Beads {
		c := color.RGBA{28, 54, 98, 255}
		if b.Label == "L4" {
			c = color.RGBA{40, 15, 80, 255}
		} else if b.Label != "R5" {
			continue
		}
		draw.Draw(dropped, image.Rect(b.X-4, b.Y-5, b.X+5, b.Y+6), image.NewUniform(c), image.Point{}, draw.Src)
	}
	first := last.CapturedAt.Add(20 * time.Millisecond)
	for i := range 2 {
		now := first.Add(time.Duration(i) * 20 * time.Millisecond)
		// Keep the same capture-source identity: changing sources must resync,
		// and would correctly discard the drop as an unobserved boundary.
		fr := frame.AnalyzeImage(dropped, eng, factory.FromApp(cfg).Mode, now, now, "native-sweep-recording")
		left, right := knownCount(fr.Beads, 'L', fr.LeftSlots), knownCount(fr.Beads, 'R', fr.RightSlots)
		if left == nil || right == nil || *left != 3 || *right != 4 {
			t.Fatalf("synthetic drop not read: %+v", fr.Beads)
		}
		tracker.observe(fr)
	}
	leftAt, _ := tracker.left.LastEvent()
	rightAt, _ := tracker.right.LastEvent()
	if tracker.left.EventCount() != 1 || tracker.right.EventCount() != 1 || !leftAt.Equal(first) || !rightAt.Equal(first) {
		t.Fatalf("real drops lost/delayed: %d/%d at %v/%v want %v", tracker.left.EventCount(), tracker.right.EventCount(), leftAt, rightAt, first)
	}
}
