package catalog

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const DefaultPath = "assets/game/substitutes.json"

type Table struct {
	Default float64
	byID    map[int]float64
	byName  map[string]float64 // only if every row for that name shares seconds
}

type file struct {
	DefaultSubstituteSeconds float64 `json:"defaultSubstituteSeconds"`
	Substitutes              []row   `json:"substitutes"`
}

type row struct {
	NinjaID int     `json:"ninjaId"`
	Name    string  `json:"name"`
	SubID   int     `json:"subId"`
	Seconds float64 `json:"seconds"`
}

func Load(path string) (*Table, error) {
	if path == "" {
		path = DefaultPath
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	t := &Table{
		Default: f.DefaultSubstituteSeconds,
		byID:    map[int]float64{},
		byName:  map[string]float64{},
	}
	if t.Default <= 0 {
		t.Default = 15
	}
	ambig := map[string]bool{}
	for _, r := range f.Substitutes {
		if r.Seconds <= 0 {
			continue
		}
		if r.NinjaID != 0 {
			t.byID[r.NinjaID] = r.Seconds
		}
		if r.SubID != 0 {
			t.byID[r.SubID] = r.Seconds
		}
		n := norm(r.Name)
		if n == "" || ambig[n] {
			continue
		}
		if prev, ok := t.byName[n]; ok && prev != r.Seconds {
			delete(t.byName, n)
			ambig[n] = true
			continue
		}
		t.byName[n] = r.Seconds
	}
	return t, nil
}

func LoadOrNil(path string) *Table {
	t, err := Load(path)
	if err != nil {
		return &Table{Default: 15, byID: map[int]float64{}, byName: map[string]float64{}}
	}
	return t
}

func (t *Table) Seconds(query string) float64 {
	if t == nil {
		return 15
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return t.Default
	}
	if n, err := strconv.Atoi(q); err == nil {
		if s, ok := t.byID[n]; ok {
			return s
		}
	}
	if s, ok := t.byName[norm(q)]; ok {
		return s
	}
	return t.Default
}

func (t *Table) Duration(query string) time.Duration {
	return time.Duration(t.Seconds(query) * float64(time.Second))
}

func norm(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "·", "")
	s = strings.ReplaceAll(s, "・", "")
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}
