package hudtext

import (
	"encoding/json"
	"sync"
)

type LibraryInfo struct{ Records, Names int }

var libraryInfoOnce sync.Once
var libraryInfo LibraryInfo

// Counts shipped records and exact base spellings, not playable heroes or accuracy.
func BundledLibraryInfo() LibraryInfo {
	libraryInfoOnce.Do(func() {
		var book struct {
			Ninjas []json.RawMessage `json:"ninjas"`
		}
		if err := json.Unmarshal(bundledNinjaCatalog, &book); err != nil {
			panic(err)
		}
		d := NewDictionary("")
		for _, entry := range hudNameExpansions {
			if d.knownBase(entry.Name) {
				d[entry.Name] = true
			}
		}
		libraryInfo = LibraryInfo{len(book.Ninjas), len(d)}
	})
	return libraryInfo
}
