package ui

import (
	"narutotimer/internal/frame"
	"time"
)

const nameDisplayGrace = 300 * time.Millisecond
const nameDisplayTTL = 2 * time.Second

// UI history only. Neither this struct nor its label is a cooldown, identity,
// slot or palette input. Keep a short name-reading gap from blinking the label.
type ninjaDisplay struct {
	name   string
	seenAt time.Time
}

func (s *session) updateNinjaDisplay(f frame.Frame) {
	at := f.CapturedAt
	if at.IsZero() {
		at = time.Now()
	}
	if !s.ninjaDisplayAt.IsZero() && at.Before(s.ninjaDisplayAt) {
		s.ninjaDisplays = [2]ninjaDisplay{}
	}
	if !s.ninjaDisplayAt.IsZero() && at.Equal(s.ninjaDisplayAt) {
		return
	}
	s.ninjaDisplayAt = at
	if f.Scene == "vs" || (!f.Hold && !f.Fighting && isEndScene(s.cfg.Scene.EndScenes, f.Scene)) {
		s.ninjaDisplays = [2]ninjaDisplay{}
		return
	}
	for i, name := range []string{f.LeftNinja, f.RightNinja} {
		current := &s.ninjaDisplays[i]
		if at.Sub(current.seenAt) > nameDisplayTTL {
			*current = ninjaDisplay{}
		}
		if !f.Fighting || f.Hold || f.Err != nil {
			continue
		}
		candidate := f.LeftNinjaCandidate
		if i == 1 {
			candidate = f.RightNinjaCandidate
		}
		if name != "" {
			*current = ninjaDisplay{name: name, seenAt: at}
		} else if candidate != "" && candidate != current.name {
			*current = ninjaDisplay{}
		}
	}
}

func (s *session) recentOpponentDisplay() (string, bool) {
	index := -1
	if s.side == "left" {
		index = 1
	}
	if s.side == "right" {
		index = 0
	}
	if index < 0 {
		return "", false
	}
	d := s.ninjaDisplays[index]
	age := s.ninjaDisplayAt.Sub(d.seenAt)
	if d.name == "" || age < 0 || age > nameDisplayTTL {
		return "", false
	}
	return d.name, age > nameDisplayGrace
}
