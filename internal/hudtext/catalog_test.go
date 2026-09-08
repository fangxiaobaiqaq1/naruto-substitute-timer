package hudtext

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBundledCatalogIsCompleteExtractedFile(t *testing.T) {
	source, err := os.ReadFile("../../assets/game/vision_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, bundledNinjaCatalog) {
		t.Fatal("embedded fallback differs from extracted source")
	}
	var catalog struct {
		Ninjas []json.RawMessage `json:"ninjas"`
	}
	if err := json.Unmarshal(source, &catalog); err != nil {
		t.Fatal(err)
	}
	d := NewDictionary("")
	if len(catalog.Ninjas) < 2000 || len(d) < 300 {
		t.Fatalf("partial library: rows=%d names=%d", len(catalog.Ninjas), len(d))
	}
	t.Logf("extracted records=%d, distinct parsed base-name entries=%d (not unique playable characters)", len(catalog.Ninjas), len(d))
	if !d["油女志乃"] {
		t.Fatal("generic name-bearing skill field was not loaded")
	}
}

func TestNewCatalogEntryNeedsNoCodeOrTemplate(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"ninjas":[{"name":"假想甲乙","skills":[{"slot":101,"name":"普通攻击_假想甲乙[试验版本]"}]}]}`)
	if err := os.WriteFile(filepath.Join(dir, "vision_catalog.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "substitutes.json")
	if err := os.WriteFile(path, []byte(`{"substitutes":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := NewDictionary(path)
	if got := d.consensus([3]string{"假想甲乙[试验版本](账号)", "假想甲乙[试验版本](账号)"}); got.Ninja != "假想甲乙[试验版本]" {
		t.Fatalf("data-only ninja not recognized: %+v", got)
	}
	if got := d.consensus([3]string{"假想甲丙", "假想甲丙"}); got.Ninja != "" || got.Candidate != "假想甲乙" {
		t.Fatalf("data-only candidate: %+v", got)
	}
	if got := NewDictionary("").parse("假想甲乙[试验版本]"); got.Ninja != "" {
		t.Fatal("test character unexpectedly hard-coded")
	}
}
