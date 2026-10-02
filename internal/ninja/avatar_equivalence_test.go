package ninja

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"io/fs"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"

	"narutotimer/assets"
	"narutotimer/internal/match"
)

var equivalenceScales = []float64{1280.0 / 960, 1.5, 2}

// sameAvatarMatch compares everything the old serial scan determines. The
// final winner loop iterates a map, so Name/BaseName/Candidate are only
// deterministic when the best score is unique (deferred tie-break).
func sameAvatarMatch(t *testing.T, label string, got, want AvatarMatch) {
	t.Helper()
	if got.ID != want.ID || got.Score != want.Score || got.RunnerUp != want.RunnerUp || !slices.Equal(got.ids, want.ids) {
		t.Fatalf("%s: got=%+v want=%+v", label, got, want)
	}
	if want.Score != want.RunnerUp && (got.Candidate != want.Candidate || got.Name != want.Name || got.BaseName != want.BaseName) {
		t.Fatalf("%s: identity got=%+v want=%+v", label, got, want)
	}
}

func TestAvatarMatchEqualsLegacySerialScan(t *testing.T) {
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	step := 1
	if testing.Short() || raceEnabled {
		step = 17
	}
	for i := 0; i < len(c.entries); i += step {
		id := c.entries[i].id
		for _, scale := range equivalenceScales {
			img, roi := perfPortraitFrame(t, c, id, scale)
			want := legacyMatch(c, img, roi, scale)
			got := c.Match(img, roi, scale)
			sameAvatarMatch(t, id+" full", got, want)
			// The <=6 shortlist path, as AvatarTracker runs between scans.
			var entries []*avatarEntry
			for _, sid := range want.ids[:min(6, len(want.ids))] {
				entries = append(entries, c.byID[sid])
			}
			view := img.SubImage(roi).(*image.RGBA)
			c.mu.Lock()
			want = legacyMatchEntries(c, view, nil, scale, entries)
			c.mu.Unlock()
			got = c.matchEntries(view, nil, scale, entries)
			sameAvatarMatch(t, id+" shortlist", got, want)
		}
	}
}

func TestAvatarGrayMaskFastPathEqualsGenericAt(t *testing.T) {
	read := func(id string) ([]byte, error) { return assets.ASAvatars.ReadFile("avatars/" + id + ".png") }
	c, err := loadAvatarCatalog(assets.ASAvatarIndex, read)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := legacyLoadAvatarCatalog(assets.ASAvatarIndex, read)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.entries) != len(legacy.entries) {
		t.Fatalf("entries %d want %d", len(c.entries), len(legacy.entries))
	}
	sameGray := func(a, b *image.Gray) bool {
		return a.Rect == b.Rect && a.Stride == b.Stride && bytes.Equal(a.Pix, b.Pix)
	}
	kinds := map[string]bool{}
	for i, e := range c.entries {
		img, _, err := image.Decode(bytes.NewReader(e.data))
		if err != nil {
			t.Fatal(err)
		}
		kinds[reflect.TypeOf(img).String()] = true
		gray, mask := avatarGrayMask(img)
		wantGray, wantMask := legacyAvatarGrayMask(img)
		if !sameGray(gray, wantGray) || !sameGray(mask, wantMask) {
			t.Fatalf("%s (%T): fast gray/mask differ from At().RGBA()", e.id, img)
		}
		// The parallel loader keeps order, metadata and every derived plane.
		l := legacy.entries[i]
		if e.id != l.id || e.name != l.name || e.baseName != l.baseName || !bytes.Equal(e.data, l.data) || legacy.byID[e.id] != l || c.byID[e.id] != e ||
			!sameGray(e.thumbGray, l.thumbGray) || !sameGray(e.thumbMask, l.thumbMask) || !sameGray(e.coarseGray, l.coarseGray) || !sameGray(e.coarseMask, l.coarseMask) ||
			!reflect.DeepEqual(e.thumb, l.thumb) {
			t.Fatalf("entry %d %s differs from serial load", i, e.id)
		}
		// Retained full-res planes equal the lazy decode matchEntries did.
		l.gray, l.mask = legacyAvatarGrayMask(img)
		l.mask = avatarFaceMask(l.gray.Bounds(), l.mask)
		if !sameGray(e.gray, l.gray) || !sameGray(e.mask, l.mask) {
			t.Fatalf("%s: retained gray/mask differ from lazy decode", e.id)
		}
	}
	if !kinds["*image.NRGBA"] || !kinds["*image.RGBA"] {
		t.Fatalf("assets no longer exercise both fast paths: %v", kinds)
	}
	// Non-zero origins and the remaining color models.
	src := image.NewNRGBA(image.Rect(3, 5, 40, 33))
	for i := range src.Pix {
		src.Pix[i] = uint8(i*37 + i/7)
	}
	rgba := image.NewRGBA(src.Rect)
	for i := range rgba.Pix {
		rgba.Pix[i] = uint8(i*53 + 11)
	}
	for _, img := range []image.Image{src, src.SubImage(image.Rect(7, 9, 30, 31)), rgba, rgba.SubImage(image.Rect(4, 6, 21, 20))} {
		gray, mask := avatarGrayMask(img)
		wantGray, wantMask := legacyAvatarGrayMask(img)
		if !sameGray(gray, wantGray) || !sameGray(mask, wantMask) {
			t.Fatalf("%T %v: fast gray/mask differ", img, img.Bounds())
		}
	}
}

func TestAvatarCatalogLoadReportsFirstFailingIndex(t *testing.T) {
	good := func(id string) ([]byte, error) { return assets.ASAvatars.ReadFile("avatars/" + id + ".png") }
	index := string(assets.ASAvatarIndex)
	for _, tc := range []struct {
		name  string
		index string
		read  func(string) ([]byte, error)
	}{
		{"corrupt", `{"entries":[{"id":"90001","ninja":"x","sha256":"0000000000000000000000000000000000000000000000000000000000000000"},{"id":"x"}]}`, func(string) ([]byte, error) { return []byte("bad"), nil }},
		{"duplicate", `{"entries":[{"id":"90001","sha256":"` + strings.Repeat("0", 64) + `"},{"id":"90001","sha256":"` + strings.Repeat("0", 64) + `"}]}`, func(string) ([]byte, error) { return nil, fs.ErrNotExist }},
		{"badhex", `{"entries":[{"id":"90001","sha256":"` + strings.Repeat("z", 64) + `"},{"id":"bad"}]}`, good},
		{"read", index, func(id string) ([]byte, error) {
			if id == "90500" || id == "90009" {
				return nil, fs.ErrNotExist
			}
			return good(id)
		}},
	} {
		_, err := loadAvatarCatalog([]byte(tc.index), tc.read)
		_, want := legacyLoadAvatarCatalog([]byte(tc.index), tc.read)
		if err == nil || want == nil || err.Error() != want.Error() {
			t.Fatalf("%s: err=%v want=%v", tc.name, err, want)
		}
	}
}

func TestItachiPortraitSubImageScoreEqualsFullFrame(t *testing.T) {
	data, err := templates.ReadFile("templates/itachi_hyakusen_portrait.png")
	if err != nil {
		t.Fatal(err)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	portraitGray := match.ToGray(src)
	for _, scale := range equivalenceScales {
		frame, _ := perfItachiFrame(t, scale)
		// Also a frame whose bounds do not start at the origin.
		padded := image.NewRGBA(image.Rect(0, 0, frame.Rect.Dx()+11, frame.Rect.Dy()+7))
		draw.Draw(padded, padded.Bounds(), image.NewUniform(color.RGBA{200, 10, 90, 255}), image.Point{}, draw.Src)
		draw.Draw(padded, frame.Rect.Add(image.Pt(11, 7)), frame, image.Point{}, draw.Src)
		shifted := padded.SubImage(frame.Rect.Add(image.Pt(11, 7))).(*image.RGBA)
		for _, img := range []*image.RGBA{frame, shifted} {
			b := img.Bounds()
			rect := image.Rect(b.Min.X+int(math.Round(847.5*scale)), b.Min.Y+int(math.Round(7.5*scale)), b.Min.X+int(math.Round(918.75*scale)), b.Min.Y+int(math.Round((7.5+71)*scale))).Intersect(b)
			// Prepare at the exact rect size so every scale reaches the score
			// (scale 2 rounds the rect one pixel wider than the template).
			portrait := match.PrepareNCC(match.ScaleGray(portraitGray, rect.Dx(), rect.Dy()), nil)
			want, err := (match.NCC{}).Match(match.Query{Image: img, Gray: match.ToGray(img), ROI: rect, Prepared: portrait})
			if err != nil {
				t.Fatal(err)
			}
			got, ok := itachiPortraitScore(img, rect, portrait)
			if !ok || got != want.Value || want.Value < .78 {
				t.Fatalf("scale=%v bounds=%v: got=%v want=%v", scale, b, got, want.Value)
			}
			size := rect.Size()
			if itachiPortraitEvidence(img, scale, portrait, size) != legacyItachiPortraitEvidence(img, scale, portrait, size) {
				t.Fatalf("scale=%v bounds=%v: evidence differs", scale, b)
			}
			blank := image.NewRGBA(b)
			if itachiPortraitEvidence(blank, scale, portrait, size) || legacyItachiPortraitEvidence(blank, scale, portrait, size) {
				t.Fatalf("scale=%v: blank portrait accepted", scale)
			}
		}
	}
}

// readerFixtures draws every embedded title template on both HUD sides at
// several scales, plus the 百战鼬 title+portrait frame with and without its
// portrait and a blank frame.
func readerFixtures(t *testing.T) []struct {
	label string
	img   *image.RGBA
	roi   image.Rectangle
	scale float64
} {
	t.Helper()
	type fixture = struct {
		label string
		img   *image.RGBA
		roi   image.Rectangle
		scale float64
	}
	var out []fixture
	files, err := fs.Glob(templates, "templates/*.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.Contains(file, "portrait") || strings.Contains(file, ".source.") {
			continue
		}
		data, err := templates.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		title, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		for _, left := range []bool{true, false} {
			base := image.NewRGBA(image.Rect(0, 0, 960, 540))
			draw.Draw(base, base.Bounds(), image.NewUniform(color.RGBA{28, 36, 49, 255}), image.Point{}, draw.Src)
			first := image.Pt(93, 61)
			if !left {
				first = image.Pt(835, 61)
			}
			roi := NameRegion(first, 1, left)
			at := image.Pt(roi.Min.X+10, roi.Min.Y+6)
			if !left {
				at.X = roi.Max.X - title.Bounds().Dx() - 10
			}
			draw.Draw(base, image.Rectangle{Min: at, Max: at.Add(title.Bounds().Size())}, title, title.Bounds().Min, draw.Src)
			for _, scale := range []float64{1, 1280.0 / 960, 1.5, 2} {
				img := base
				if scale != 1 {
					img = image.NewRGBA(image.Rect(0, 0, int(960*scale+.5), int(540*scale+.5)))
					xdraw.BiLinear.Scale(img, img.Bounds(), base, base.Bounds(), draw.Src, nil)
				}
				p := image.Pt(int(float64(first.X)*scale+.5), int(float64(first.Y)*scale+.5))
				out = append(out, fixture{file, img, NameRegion(p, scale, left), scale})
			}
		}
	}
	for _, scale := range []float64{1, 1280.0 / 960, 1.5} {
		img, roi := perfItachiFrame(t, scale)
		out = append(out, fixture{"itachi", img, roi, scale})
		occluded := image.NewRGBA(img.Bounds())
		copy(occluded.Pix, img.Pix)
		b := img.Bounds()
		draw.Draw(occluded, image.Rect(int(840*scale), 0, b.Max.X, int(90*scale)), image.Black, image.Point{}, draw.Src)
		out = append(out, fixture{"itachi-no-portrait", occluded, roi, scale})
	}
	blank := image.NewRGBA(image.Rect(0, 0, 960, 540))
	out = append(out, fixture{"blank", blank, NameRegion(image.Pt(93, 61), 1, true), 1})
	return out
}

func TestReaderReadEqualsSequentialReference(t *testing.T) {
	r := NewReader()
	recognized := 0
	for _, f := range readerFixtures(t) {
		// The reference runs first so both share the prepared templates (and
		// therefore pointer-equal evidence.template/portrait).
		want := legacyRead(r, f.img, f.roi, f.scale)
		got := r.read(f.img, f.roi, f.scale)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s scale=%v roi=%v: got=%+v want=%+v", f.label, f.scale, f.roi, got, want)
		}
		if want.Name != "" {
			recognized++
		}
	}
	if recognized < 20 {
		t.Fatalf("fixtures recognized only %d titles; equivalence is vacuous", recognized)
	}
}

func TestAvatarTrackersShareCatalogConcurrently(t *testing.T) {
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	type side struct {
		img   *image.RGBA
		roi   image.Rectangle
		scale float64
	}
	// Different scales also make the trackers contend for the template cache
	// (scale changes evict it) while other goroutines score unlocked.
	sides := []side{}
	for i, id := range []string{"90511", "90464", "920541", "90021"} {
		img, roi := perfPortraitFrame(t, c, id, equivalenceScales[i%len(equivalenceScales)])
		sides = append(sides, side{img, roi, equivalenceScales[i%len(equivalenceScales)]})
	}
	frames := []time.Duration{0, 16 * time.Millisecond, 32 * time.Millisecond, 600 * time.Millisecond, 616 * time.Millisecond}
	at := time.Unix(1700000000, 0)
	run := func(s side) ([]AvatarMatch, AvatarMatch) {
		var tracker AvatarTracker
		var out []AvatarMatch
		for _, dt := range frames {
			// Clear the pixel memo so every frame really scores.
			tracker.memoOK = false
			out = append(out, tracker.Read(c, s.img, s.roi, s.scale, at.Add(dt)))
		}
		return out, c.Match(s.img, s.roi, s.scale)
	}
	want := make([][]AvatarMatch, len(sides))
	wantMatch := make([]AvatarMatch, len(sides))
	for i, s := range sides {
		want[i], wantMatch[i] = run(s)
		if want[i][0].ID == "" {
			t.Fatalf("side %d fixture unrecognized: %+v", i, want[i][0])
		}
	}
	rounds := 3
	if testing.Short() || raceEnabled {
		rounds = 1
	}
	got := make([][][]AvatarMatch, len(sides))
	gotMatch := make([][]AvatarMatch, len(sides))
	var wg sync.WaitGroup
	for i, s := range sides {
		wg.Go(func() {
			for range rounds {
				reads, m := run(s)
				got[i], gotMatch[i] = append(got[i], reads), append(gotMatch[i], m)
			}
		})
	}
	wg.Wait()
	for i := range sides {
		for round := range rounds {
			for f := range frames {
				g, w := got[i][round][f], want[i][f]
				if g.ID != w.ID || g.Score != w.Score || g.RunnerUp != w.RunnerUp || !slices.Equal(g.ids, w.ids) {
					t.Fatalf("side %d round %d frame %d: got=%+v want=%+v", i, round, f, g, w)
				}
			}
			if g, w := gotMatch[i][round], wantMatch[i]; g.ID != w.ID || g.Score != w.Score || g.RunnerUp != w.RunnerUp || !slices.Equal(g.ids, w.ids) {
				t.Fatalf("side %d round %d Match: got=%+v want=%+v", i, round, g, w)
			}
		}
	}
}
