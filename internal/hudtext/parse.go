package hudtext

import (
	_ "embed"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type Title struct {
	Ninja   string
	Account string
	// Candidate is display-only, never confirmed identity or version evidence.
	Candidate string
}
type Dictionary map[string]bool

//go:embed data/vision_catalog.json
var bundledNinjaCatalog []byte

// Exact complete HUD spellings documented against local capture evidence. This
// supplements abbreviated catalog entries; it never strips OCR noise or fixes
// glyphs, and never supplies a ninja version or gameplay parameters.
//
//go:embed data/hud_names.json
var verifiedHUDNames []byte

var hudNameExpansions = func() []struct {
	Name        string `json:"name"`
	CatalogBase string `json:"catalog_base"`
} {
	var names []struct {
		Name        string `json:"name"`
		CatalogBase string `json:"catalog_base"`
	}
	if err := json.Unmarshal(verifiedHUDNames, &names); err != nil {
		panic(err)
	}
	return names
}()

// Load the extracted catalog, never a hand-written ninja list or its cooldowns.
func NewDictionary(path string) Dictionary {
	d := Dictionary{}
	d.addCatalog(bundledNinjaCatalog)
	if path != "" {
		d.loadCatalog(filepath.Join(filepath.Dir(path), "vision_catalog.json"))
		d.loadCatalog(path)
	}
	return d
}

func (d Dictionary) loadCatalog(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return
	}
	d.addCatalog(data)
}

func (d Dictionary) addCatalog(data []byte) {
	var book struct {
		Substitutes []struct {
			Name string `json:"name"`
		} `json:"substitutes"`
		Ninjas []struct {
			Name   string `json:"name"`
			Skills []struct {
				Slot int    `json:"slot"`
				Name string `json:"name"`
			} `json:"skills"`
		} `json:"ninjas"`
	}
	if json.Unmarshal(data, &book) != nil {
		return
	}
	for _, r := range book.Substitutes {
		d.addName(strings.TrimPrefix(strings.TrimSpace(r.Name), "替身术_"))
	}
	for _, r := range book.Ninjas {
		d.addName(r.Name)
		for _, s := range r.Skills {
			if s.Slot != 101 && s.Slot != 501 && s.Slot != 601 && s.Slot != 901 {
				continue
			}
			for _, role := range []string{"普通攻击", "密卷技", "通灵兽技", "替身术"} {
				if strings.HasPrefix(s.Name, role+"_") {
					d.addName(strings.TrimPrefix(s.Name, role+"_"))
				}
				if strings.HasSuffix(s.Name, role) {
					d.addName(strings.TrimSuffix(s.Name, role))
				}
			}
		}
	}
}

func (d Dictionary) addName(name string) {
	base := strings.Split(compact(name), "[")[0]
	if validHan(base, 2, 10) {
		d[base] = true
	}
}

func validHan(s string, minimum, maximum int) bool {
	n := 0
	for _, r := range s {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
		n++
	}
	return n >= minimum && n <= maximum
}

func compact(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		switch r {
		case '［', '【', '〔':
			return '['
		case '］', '】', '〕':
			return ']'
		case '（', '{':
			return '('
		case '）', '}':
			return ')'
		}
		return r
	}, s)
}

// parse is intentionally not fuzzy. In particular 白/自, 冥/具 and a partial
// name are NOT interchangeable. The full version must survive all checks.
func (d Dictionary) parse(raw string) Title {
	text := compact(raw)
	var title Title
	head := text
	if open := strings.LastIndex(text, "("); open >= 0 {
		head = text[:open]
		if strings.HasSuffix(text, ")") {
			account := text[open+1 : len(text)-1]
			if validAccount(account) {
				title.Account = account
			}
		}
	}
	base := head
	if i := strings.IndexAny(base, "[()"); i >= 0 {
		base = base[:i]
	}
	if !d.knownBase(base) {
		return title
	}
	title.Ninja = base
	if head == base {
		return title
	}
	if strings.HasPrefix(head, base+"[") && strings.HasSuffix(head, "]") {
		version := head[len(base)+1 : len(head)-1]
		clean := strings.ReplaceAll(strings.ReplaceAll(version, "·", ""), "・", "")
		if validHan(clean, 2, 16) {
			title.Ninja = base + "[" + version + "]"
		}
	}
	return title
}

func validAccount(s string) bool {
	n := 0
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_·.-", r) {
			return false
		}
		n++
	}
	return n >= 2 && n <= 24
}

// Two visual treatments must agree exactly on the account. A full ninja
// version similarly needs two matching votes; otherwise a known base name may
// be displayed, but a base name cannot activate the five-kage dual-clock rule.
func (d Dictionary) consensus(raw [variants]string) Title {
	accounts, names, bases := map[string]int{}, map[string]int{}, map[string]int{}
	candidates := map[string]int{}
	for _, text := range raw {
		if candidate := d.candidate(text); candidate != "" {
			candidates[candidate]++
		}
		p := d.parse(text)
		if p.Account != "" {
			accounts[p.Account]++
		}
		if p.Ninja != "" {
			names[p.Ninja]++
			bases[strings.Split(p.Ninja, "[")[0]]++
		}
	}
	majority := func(counts map[string]int) string {
		for name, n := range counts {
			if n >= 2 {
				return name
			}
		}
		return ""
	}
	out := Title{Account: majority(accounts), Ninja: majority(names)}
	if out.Ninja == "" {
		out.Ninja = majority(bases)
	}
	if out.Ninja == "" {
		out.Candidate = majority(candidates)
	}
	return out
}
