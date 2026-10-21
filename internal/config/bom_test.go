package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// GE-11: Windows editors (Notepad) may save config.json as UTF-8 with a byte
// order mark. The app then refused to start: "invalid character 'ï' looking
// for beginning of value". The mark is skipped; the decoder stays strict.
func TestLoadSkipsUTF8ByteOrderMark(t *testing.T) {
	cfg := Default()
	cfg.UI.PlayerNames = []string{"白方小"}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, text string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	loaded, err := Load(write("bom.json", "\xef\xbb\xbf"+string(data)))
	if err != nil {
		t.Fatalf("BOM config rejected: %v", err)
	}
	if !slices.Equal(loaded.UI.PlayerNames, cfg.UI.PlayerNames) {
		t.Fatalf("names = %q", loaded.UI.PlayerNames)
	}
	unknown := strings.Replace(string(data), `"schemaVersion": 1,`, `"schemaVersion": 1, "futureOption": true,`, 1)
	if _, err := Load(write("bom-unknown.json", "\xef\xbb\xbf"+unknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field after a BOM: %v", err)
	}
	if _, err := Load(write("two-boms.json", "\xef\xbb\xbf\xef\xbb\xbf"+string(data))); err == nil {
		t.Fatal("only one leading byte order mark is skipped")
	}
}
