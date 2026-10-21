package ninja

import (
	"io/fs"
	"os"
	"testing"

	"narutotimer/assets"
)

// diskTemplates reads internal/ninja/templates from the source tree: tests
// also use crops (sources, ordinary names) the runtime does not embed.
var diskTemplates = os.DirFS(".").(fs.ReadFileFS)

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

// Every embedded name strip is loaded and every strip the reader loads is
// embedded: a missing embed would silently drop a template.
func TestEmbeddedNameTemplatesAllLoad(t *testing.T) {
	entries, err := fs.ReadDir(templates, "templates")
	if err != nil {
		t.Fatal(err)
	}
	r := NewReader()
	portrait := 0
	for _, s := range r.source {
		if s.portrait != nil {
			portrait++
		}
	}
	// 14 title strips plus the 百战鼬 portrait (shared by its two strips).
	if len(r.source) != 14 || portrait != 2 || len(entries) != 15 {
		t.Fatalf("reader loaded %d strips (%d with portrait); %d files embedded", len(r.source), portrait, len(entries))
	}
}
