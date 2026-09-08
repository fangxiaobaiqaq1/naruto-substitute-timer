package app

// ObservationSpace remembers the known capture coordinate system. Missing
// dimensions or method on an error frame do not erase the last known values.
type ObservationSpace struct {
	width, height int
	method        string
}

// Update reports a change only when a previously known field changes. Call it
// before rejecting error/hold frames: they can already carry a new geometry.
func (s *ObservationSpace) Update(width, height int, method string) bool {
	changed := false
	if width > 0 && height > 0 {
		changed = s.width > 0 && s.height > 0 && (s.width != width || s.height != height)
		s.width, s.height = width, height
	}
	if method != "" {
		changed = changed || (s.method != "" && s.method != method)
		s.method = method
	}
	return changed
}
