package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupIDAndUniqueName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	raw := `{
	  "defaultSubstituteSeconds": 15,
	  "substitutes": [
	    {"ninjaId": 904110, "name": "创立柱间", "subId": 904110901, "seconds": 15},
	    {"ninjaId": 980230, "name": "创立柱间", "subId": 980230901, "seconds": 10},
	    {"ninjaId": 908010, "name": "疾风咒佐限定", "subId": 908010901, "seconds": 10}
	  ]
	}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	tab, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if tab.Seconds("904110") != 15 || tab.Seconds("980230") != 10 {
		t.Fatalf("id lookup failed: %v %v", tab.Seconds("904110"), tab.Seconds("980230"))
	}
	if tab.Seconds("创立柱间") != 15 {
		t.Fatalf("ambiguous name must fall back to default 15, got %v", tab.Seconds("创立柱间"))
	}
	if tab.Seconds("疾风咒佐限定") != 10 {
		t.Fatalf("unique name should hit 10, got %v", tab.Seconds("疾风咒佐限定"))
	}
	if tab.Seconds("") != 15 || tab.Seconds("没有这个人") != 15 {
		t.Fatalf("unknown must use default")
	}
}
