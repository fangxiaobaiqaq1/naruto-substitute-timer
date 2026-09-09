package hudtext

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"narutotimer/assets"
)

func TestBundledCatalogsContainTheCompleteExtractedData(t *testing.T) {
	for _, tc := range []struct {
		name     string
		embedded []byte
	}{
		{"vision_catalog.json", bundledNinjaCatalog}, {"substitutes.json", assets.SubstituteCatalog},
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", "assets", "game", tc.name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, tc.embedded) {
			t.Fatalf("%s embedded copy differs from extracted catalog", tc.name)
		}
	}
	// No user table or external asset directory is present at this path.
	d := NewDictionary(filepath.Join(t.TempDir(), "assets", "substitutes.json"))
	for _, name := range []string{"萨克·镫", "神秘面具男", "宇智波带土"} {
		if !d.knownBase(name) {
			t.Fatalf("missing standalone name %s", name)
		}
	}
	info := BundledLibraryInfo()
	if info.Records != 2490 || info.Names < 500 {
		t.Fatalf("incomplete embedded library: %+v", info)
	}
	t.Logf("embedded records=%d exact base names=%d", info.Records, info.Names)
}
