package ui

import (
	"errors"
	"image"
	"testing"
	"time"

	timerapp "narutotimer/internal/app"
	"narutotimer/internal/frame"
)

func TestCaptureOnceSourceChangesKeepCooldownAndResyncBaseline(t *testing.T) {
	oldImage := image.NewRGBA(image.Rect(0, 0, 1600, 900))
	newImage := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	for _, name := range []string{"resolution", "method", "both", "error then missing metadata", "unknown beads"} {
		t.Run(name, func(t *testing.T) {
			base := time.Unix(1700000000, 0)
			at := func(n int) time.Time { return base.Add(time.Duration(n) * 16 * time.Millisecond) }
			makeFrame := func(n, ready int, img *image.RGBA, method string) frame.Frame {
				return frame.Frame{Img: img, CaptureMethod: method, CapturedAt: at(n), Fighting: true, Scene: "fight", LayoutProfile: "duel", Beads: testBeads(ready, ready)}
			}
			changedImage, changedMethod := newImage, "mumu-sdk"
			if name == "method" {
				changedImage = oldImage
			}
			if name == "method" || name == "both" {
				changedMethod = "printwindow"
			}
			frames := []frame.Frame{
				makeFrame(0, 4, oldImage, "mumu-sdk"),
				makeFrame(1, 3, oldImage, "mumu-sdk"),
				makeFrame(2, 3, oldImage, "mumu-sdk"),
			}
			changed := makeFrame(3, 2, changedImage, changedMethod)
			switch name {
			case "error then missing metadata":
				changed.Err = errors.New("capture geometry is changing")
				frames = append(frames, changed, frame.Frame{CapturedAt: at(4), Err: errors.New("capture unavailable")})
			case "unknown beads":
				changed.Beads[0].Unknown = true
				changed.Beads[4].Unknown = true
				frames = append(frames, changed)
			default:
				frames = append(frames, changed)
			}
			baselineIndex := len(frames)
			frames = append(frames, makeFrame(baselineIndex, 2, changedImage, changedMethod), makeFrame(baselineIndex+1, 2, changedImage, changedMethod))
			dropIndex := len(frames)
			frames = append(frames, makeFrame(dropIndex, 1, changedImage, changedMethod), makeFrame(dropIndex+1, 1, changedImage, changedMethod))
			s := testSession(frames)
			for i := 0; i < dropIndex; i++ {
				s.captureOnce()
			}
			for side, clock := range map[string]*timerapp.SideClock{"left": &s.left, "right": &s.right} {
				first, serial := clock.LastEvent()
				if serial != 1 || !first.Equal(at(1)) || clock.LastReady() != 2 || len(clock.Remaining(at(dropIndex-1))) != 1 {
					t.Fatalf("%s source change reset or invented cooldown: serial=%d first=%s ready=%d", side, serial, first, clock.LastReady())
				}
			}
			s.captureOnce()
			s.captureOnce()
			for side, clock := range map[string]*timerapp.SideClock{"left": &s.left, "right": &s.right} {
				first, serial := clock.LastEvent()
				if serial != 2 || !first.Equal(at(dropIndex)) || len(clock.Remaining(at(dropIndex+1))) != 2 {
					t.Fatalf("%s post-change drop missing or delayed: serial=%d first=%s", side, serial, first)
				}
			}
		})
	}
}
