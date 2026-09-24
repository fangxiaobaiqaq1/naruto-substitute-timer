package ninja

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	_ "image/png"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
	"narutotimer/assets"
)

func TestEmbeddedASAvatarCatalogIsBoundedAndValid(t *testing.T) {
	stats, err := AvatarStats()
	if err != nil || stats.Entries != 242 || stats.Base != 183 || stats.Skins != 59 || stats.Bytes != 3009250 {
		t.Fatalf("embedded stats=%+v err=%v", stats, err)
	}
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.entries) != 242 {
		t.Fatalf("embedded avatar count=%d, want 242", len(c.entries))
	}
	if _, err := assets.ASAvatars.ReadFile("avatars/90009.png"); err != nil {
		t.Fatalf("embedded asset missing: %v", err)
	}
	base, skins := 0, 0
	for _, e := range c.entries {
		if len(e.id) == 5 {
			base++
		} else {
			skins++
		}
	}
	if base != 183 || skins != 59 {
		t.Fatalf("base/skin=%d/%d, want 183/59", base, skins)
	}
}

func TestEmbeddedAvatarManifestMapsEveryIDToCanonicalMetadata(t *testing.T) {
	var manifest struct {
		Entries []avatarIndexEntry `json:"entries"`
	}
	if err := json.Unmarshal(assets.ASAvatarIndex, &manifest); err != nil {
		t.Fatal(err)
	}
	catalog, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != len(catalog.entries) {
		t.Fatalf("manifest/catalog %d/%d", len(manifest.Entries), len(catalog.entries))
	}
	seen := map[string]bool{}
	for _, item := range manifest.Entries {
		if item.ID == "" || seen[item.ID] || item.Ninja == "" {
			t.Fatalf("bad ID/name mapping: %+v", item)
		}
		seen[item.ID] = true
		if item.IsSkin && (item.BaseNinjaID == "" || item.BaseNinjaName == "") {
			t.Fatalf("skin lacks explicit base mapping: %+v", item)
		}
		entry := catalog.byID[item.ID]
		if entry == nil || entry.name != avatarName(item) {
			t.Fatalf("ID %s catalog=%+v want=%q", item.ID, entry, avatarName(item))
		}
		if _, err := assets.ASAvatars.ReadFile("avatars/" + item.ID + ".png"); err != nil {
			t.Fatalf("ID %s missing embedded png: %v", item.ID, err)
		}
	}
}

func TestAvatarIndexRejectsCorruptAsset(t *testing.T) {
	data := []byte(`{"entries":[{"id":"90001","ninja":"x","sha256":"0000000000000000000000000000000000000000000000000000000000000000"}]}`)
	if _, err := loadAvatarCatalog(data, func(string) ([]byte, error) { return []byte("bad"), nil }); err == nil {
		t.Fatal("corrupt avatar accepted")
	}
}

func TestAvatarRegionUsesOneReferenceGeometry(t *testing.T) {
	for _, width := range []int{1280, 1600, 1920, 2560} {
		s := float64(width) / 960
		left := AvatarRegion(image.Pt(int(93*s), int(61*s)), s, true)
		right := AvatarRegion(image.Pt(int(835*s), int(61*s)), s, false)
		if left.Empty() || right.Empty() || left.Dx() < int(60*s)-2 || right.Dx() < int(60*s)-2 {
			t.Fatalf("width %d: left=%v right=%v", width, left, right)
		}
	}
}

func TestAvatarCurrentPixelsDoNotInherit(t *testing.T) {
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	entry := c.byID["90511"]
	if entry == nil {
		t.Fatal("missing Ten-Tails Obito")
	}
	portrait, _, err := image.Decode(bytes.NewReader(entry.data))
	if err != nil {
		t.Fatal(err)
	}
	b := portrait.Bounds().Size()
	img := image.NewRGBA(image.Rect(0, 0, b.X+20, b.Y+20))
	roi := image.Rectangle{Min: image.Pt(10, 10), Max: image.Pt(10+b.X, 10+b.Y)}
	draw.Draw(img, roi, portrait, portrait.Bounds().Min, draw.Src)
	var tracker AvatarTracker
	at := time.Unix(1700000000, 0)
	if got := tracker.Read(c, img, roi, 1, at); got.ID != entry.id {
		t.Fatalf("fixture avatar did not match %s: %+v", entry.id, got)
	}
	draw.Draw(img, img.Bounds(), image.Black, image.Point{}, draw.Src)
	if got := tracker.Read(c, img, roi, 1, at.Add(time.Millisecond)); got.Name != "" {
		t.Fatalf("stale avatar survived: %+v", got)
	}
}

func TestAvatarRecognitionSurvivesScaledLeftAndRightROI(t *testing.T) {
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"90511", "90464", "920541"} {
		entry := c.byID[id]
		portrait, _, err := image.Decode(bytes.NewReader(entry.data))
		if err != nil {
			t.Fatal(err)
		}
		for _, scale := range []float64{1280.0 / 960, 1600.0 / 960, 1920.0 / 960, 2560.0 / 960} {
			w, h := int(float64(portrait.Bounds().Dx())*scale+.5), int(float64(portrait.Bounds().Dy())*scale+.5)
			for _, left := range []bool{true, false} {
				img := image.NewRGBA(image.Rect(0, 0, w+40, h+40))
				draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{21, 47, 68, 255}), image.Point{}, draw.Src)
				roi := image.Rect(20, 20, 20+w, 20+h)
				xdraw.BiLinear.Scale(img, roi, portrait, portrait.Bounds(), draw.Over, nil)
				if got := c.Match(img, roi, scale); got.ID != id {
					t.Fatalf("%s scale=%g left=%v: %+v", id, scale, left, got)
				}
			}
		}
	}
}

func TestAvatarTitleFusionRequiresCurrentAgreement(t *testing.T) {
	all, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	makeOne := func(id string) *avatarCatalog {
		e := all.byID[id]
		return &avatarCatalog{entries: []*avatarEntry{e}, byID: map[string]*avatarEntry{id: e}}
	}
	obito, sasuke := makeOne("90511"), makeOne("90464")
	portrait, _, err := image.Decode(bytes.NewReader(sasuke.byID["90464"].data))
	if err != nil {
		t.Fatal(err)
	}
	b := portrait.Bounds()
	img := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(img, img.Bounds(), portrait, b.Min, draw.Src)
	at := time.Unix(1700000000, 0)
	title := Readout{Name: Obito, Slots: 4, Palette: Purple}
	r := &Reader{avatars: sasuke}
	var tracker AvatarTracker
	if got := r.ResolveEvidence(img, image.Rectangle{}, img.Bounds(), 1, title, &tracker, at); got.Name != "" {
		t.Fatalf("title/avatar conflict confirmed: %+v", got)
	}
	r.avatars = obito
	portrait, _, err = image.Decode(bytes.NewReader(obito.byID["90511"].data))
	if err != nil {
		t.Fatal(err)
	}
	img = image.NewRGBA(portrait.Bounds())
	draw.Draw(img, img.Bounds(), portrait, portrait.Bounds().Min, draw.Src)
	tracker = AvatarTracker{}
	if got := r.ResolveEvidence(img, image.Rectangle{}, img.Bounds(), 1, title, &tracker, at); got.Name != Obito {
		t.Fatalf("agreement not confirmed: %+v", got)
	}
	tracker = AvatarTracker{}
	if got := r.ResolveEvidence(img, image.Rectangle{}, img.Bounds(), 1, Readout{}, &tracker, at); got.Name != Obito || got.Slots != 4 || got.Palette != Purple {
		t.Fatalf("obscured title did not use strong current avatar: %+v", got)
	}
	blank := image.NewRGBA(img.Bounds())
	tracker = AvatarTracker{}
	if got := r.ResolveEvidence(blank, image.Rectangle{}, blank.Bounds(), 1, Readout{}, &tracker, at); got.Name != "" {
		t.Fatalf("weak evidence became identity: %+v", got)
	}
}

func TestHyakusenCorpusIDIsExactSkinMapping(t *testing.T) {
	var manifest struct {
		Entries []avatarIndexEntry `json:"entries"`
	}
	if err := json.Unmarshal(assets.ASAvatarIndex, &manifest); err != nil {
		t.Fatal(err)
	}
	var found avatarIndexEntry
	for _, item := range manifest.Entries {
		if item.ID == "920541" {
			found = item
			break
		}
	}
	if !found.IsSkin || found.BaseNinjaID != "90054" || found.CanonicalName != "宇智波鼬[百战]" {
		t.Fatalf("ID 920541 mapping=%+v", found)
	}
}

func TestHyakusenItachiOffsetRequiresExactCurrentAvatar(t *testing.T) {
	all, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	one := func(id string) *avatarCatalog {
		e := all.byID[id]
		return &avatarCatalog{entries: []*avatarEntry{e}, byID: map[string]*avatarEntry{id: e}}
	}
	itachi := one("920541")
	portrait, _, err := image.Decode(bytes.NewReader(itachi.byID["920541"].data))
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(portrait.Bounds())
	draw.Draw(img, img.Bounds(), portrait, portrait.Bounds().Min, draw.Src)
	r := &Reader{avatars: itachi}
	var tracker AvatarTracker
	if got := r.ResolveEvidence(img, image.Rectangle{}, img.Bounds(), 1, Readout{}, &tracker, time.Unix(1, 0)); got.Name == "" || got.Slots != 4 || got.RowOffsetY != EnergyGaugeRowOffset {
		t.Fatalf("exact current 百战鼬 did not offset: %+v", got)
	}
	base := one("90022")
	portrait, _, err = image.Decode(bytes.NewReader(base.byID["90022"].data))
	if err != nil {
		t.Fatal(err)
	}
	img = image.NewRGBA(portrait.Bounds())
	draw.Draw(img, img.Bounds(), portrait, portrait.Bounds().Min, draw.Src)
	r.avatars, tracker = base, AvatarTracker{}
	if got := r.ResolveEvidence(img, image.Rectangle{}, img.Bounds(), 1, Readout{}, &tracker, time.Unix(1, 0)); got.RowOffsetY != 0 {
		t.Fatalf("base Itachi enabled 百战 offset: %+v", got)
	}
}

func BenchmarkAvatarMatch(b *testing.B) {
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		b.Fatal(err)
	}
	entry := c.byID["90511"]
	portrait, _, err := image.Decode(bytes.NewReader(entry.data))
	if err != nil {
		b.Fatal(err)
	}
	img := image.NewRGBA(portrait.Bounds())
	draw.Draw(img, img.Bounds(), portrait, portrait.Bounds().Min, draw.Src)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		c.Match(img, img.Bounds(), 1)
	}
}

func TestOrochimaruVariantsDoNotCrossMatch(t *testing.T) {
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"90021", "90191", "90524"} {
		entry := c.byID[id]
		portrait, _, err := image.Decode(bytes.NewReader(entry.data))
		if err != nil {
			t.Fatal(err)
		}
		b := portrait.Bounds().Size()
		img := image.NewRGBA(image.Rect(0, 0, b.X+20, b.Y+20))
		roi := image.Rectangle{Min: image.Pt(10, 10), Max: image.Pt(10+b.X, 10+b.Y)}
		draw.Draw(img, roi, portrait, portrait.Bounds().Min, draw.Src)
		got := c.Match(img, roi, 1)
		if got.ID != id {
			t.Fatalf("avatar %s cross-matched: %+v", id, got)
		}
	}
}
