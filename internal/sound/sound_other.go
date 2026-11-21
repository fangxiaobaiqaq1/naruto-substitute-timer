//go:build !windows

package sound

// Desktop audio is Windows-only; other builds accept the call silently.
func playDefault() error         { return nil }
func playFile(path string) error { return nil }
