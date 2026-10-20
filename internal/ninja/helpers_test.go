package ninja

import (
	"testing"

	"narutotimer/assets"
)

// testAvatarPNG reads an embedded catalog portrait; the catalog itself no
// longer retains PNG bytes after decoding them.
func testAvatarPNG(tb testing.TB, id string) []byte {
	tb.Helper()
	data, err := assets.ASAvatars.ReadFile("avatars/" + id + ".png")
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

// testPrepared returns the most recently used prepared name templates.
func (r *Reader) testPrepared() []scaledName {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.scales) == 0 {
		return nil
	}
	return r.scales[0].prepared
}
