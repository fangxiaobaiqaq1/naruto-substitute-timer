package ninja

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"
	"time"

	xdraw "golang.org/x/image/draw"
)

// hudPortraitFrame draws catalog portrait id the way the live HUD shows it:
// scaled to factor times the bead-derived scale, centred on the measured
// diamond centre of the left or right AvatarRegion window, over a dark red
// diamond frame of avatarBoxSide units and a two-colour scene. It returns the
// frame and the window. id "" draws the frame without a portrait.
func hudPortraitFrame(tb testing.TB, c *avatarCatalog, id string, scale, factor float64, left bool) (*image.RGBA, image.Rectangle) {
	tb.Helper()
	first := image.Pt(int(math.Round(93*scale)), int(math.Round(61*scale)))
	cx := avatarBoxLeftX
	if !left {
		first.X = int(math.Round(835 * scale))
		cx = avatarBoxRightX
	}
	window := AvatarRegion(first, scale, left)
	img := image.NewRGBA(image.Rect(0, 0, int(math.Round(960*scale)), int(math.Round(120*scale))))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(60 + 90*y/img.Bounds().Dy()), uint8(110 + 40*x/img.Bounds().Dx()), 170, 255})
		}
	}
	centre := image.Pt(window.Min.X+int(math.Round(cx*scale)), window.Min.Y+int(math.Round(avatarBoxCentreY*scale)))
	half := avatarBoxSide / 2 * scale
	for y := centre.Y - int(half); y <= centre.Y+int(half); y++ {
		for x := centre.X - int(half); x <= centre.X+int(half); x++ {
			if d := math.Abs(float64(x-centre.X)) + math.Abs(float64(y-centre.Y)); d <= half && image.Pt(x, y).In(img.Bounds()) {
				shade := color.RGBA{58, 20, 22, 255}
				if d >= half-4*scale {
					shade = color.RGBA{150, 40, 36, 255}
				}
				img.SetRGBA(x, y, shade)
			}
		}
	}
	if id == "" {
		return img, window
	}
	portrait, _, err := image.Decode(bytes.NewReader(testAvatarPNG(tb, id)))
	if err != nil {
		tb.Fatal(err)
	}
	w := int(math.Round(float64(portrait.Bounds().Dx()) * scale * factor))
	h := int(math.Round(float64(portrait.Bounds().Dy()) * scale * factor))
	at := centre.Sub(image.Pt(w/2, h/2))
	xdraw.CatmullRom.Scale(img, image.Rectangle{Min: at, Max: at.Add(image.Pt(w, h))}, portrait, portrait.Bounds(), draw.Over, nil)
	return img, window
}

// hudPortraitCases are portraits of both icon generations (70px and 85px
// catalog assets) whose real HUD portraits were measured, each drawn at
// display factors across its generation's measured range.
var hudPortraitCases = []struct {
	id      string
	factors []float64
}{
	{"90174", []float64{.66, .72, .78}}, // 神秘面具男 (user video 15)
	{"90082", []float64{.66, .72, .78}}, // 药师兜[半蛇斗篷]
	{"90230", []float64{.66, .72, .78}}, // 春野樱[百豪]
	{"90321", []float64{.66, .72, .78}}, // 佩恩·天道[超]
	{"90106", []float64{.66, .72, .78}}, // 宇智波斑[秽土转生]
	{"90464", []float64{.50, .54}},      // 宇智波佐助[侠隐江湖]
	{"99052", []float64{.50, .54}},      // 大筒木浦式[异星钓者]
	{"90244", []float64{.50, .54}},      // 旗木卡卡西[须佐能乎]
}

var hudPortraitScales = []float64{1280.0 / 960, 1600.0 / 960, 2}

// The live HUD shows a 70px icon at about 0.72x the bead-derived scale and an
// 85px icon at about 0.55x. The full scan alone (one 0.60 factor, a locator
// thumbnail of the whole diamond neighbourhood) missed most 70px portraits
// drawn like that; its second look finds every one, on both sides, and the
// tracker keeps the identity on the frames between full scans.
func TestHUDPortraitsOfBothIconGenerations(t *testing.T) {
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	missed70, total70 := 0, 0
	for _, tc := range hudPortraitCases {
		for _, scale := range hudPortraitScales {
			for _, factor := range tc.factors {
				for _, left := range []bool{true, false} {
					label := fmt.Sprintf("%s scale=%.3f factor=%.2f left=%v", tc.id, scale, factor, left)
					img, window := hudPortraitFrame(t, c, tc.id, scale, factor, left)
					if got := c.Match(img, window, scale); got.ID != tc.id {
						t.Errorf("%s: got %+v", label, got)
					}
					if c.byID[tc.id].gray.Bounds().Dx() < 80 {
						total70++
						if scan := legacyScan(c, img, window, scale); scan.ID != tc.id {
							missed70++
						}
					}
					var tracker AvatarTracker
					at := time.Unix(1700000000, 0)
					for frame := range 3 {
						tracker.memoOK = false // score every frame
						if got := tracker.Read(c, img, window, scale, at.Add(time.Duration(frame)*100*time.Millisecond)); got.ID != tc.id {
							t.Errorf("%s frame %d: tracker got %+v", label, frame, got)
						}
					}
				}
			}
		}
	}
	t.Logf("the full scan alone missed %d of %d 70px portraits", missed70, total70)
	if missed70*2 < total70 {
		t.Fatalf("the full scan alone found %d of %d 70px portraits; the fixture no longer shows the miss", total70-missed70, total70)
	}
}

// Negative controls: a portrait whose ninja is not in the catalog (every
// form of that ninja removed) gets no name, and neither does an empty
// diamond. 宇智波佐助 is not used here: without him in the catalog his
// portraits already pass the strict gate as 宇智波泉奈 (90372), a lookalike,
// in the full scan alone.
func TestHUDPortraitUncataloguedStaysUnknown(t *testing.T) {
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range hudPortraitCases {
		e := c.byID[tc.id]
		if e.baseName == "宇智波佐助" {
			continue
		}
		without := &avatarCatalog{byID: map[string]*avatarEntry{}}
		for _, o := range c.entries {
			if o.baseName != e.baseName {
				without.entries = append(without.entries, o)
				without.byID[o.id] = o
			}
		}
		for _, scale := range hudPortraitScales {
			for _, factor := range tc.factors {
				for _, left := range []bool{true, false} {
					img, window := hudPortraitFrame(t, c, tc.id, scale, factor, left)
					if got := without.Match(img, window, scale); got.ID != "" {
						t.Errorf("%s (%s) scale=%.3f factor=%.2f left=%v: named %+v", tc.id, e.baseName, scale, factor, left, got)
					}
				}
			}
		}
	}
	for _, scale := range hudPortraitScales {
		for _, left := range []bool{true, false} {
			img, window := hudPortraitFrame(t, c, "", scale, 0, left)
			if got := c.Match(img, window, scale); got.ID != "" {
				t.Errorf("empty diamond scale=%.3f left=%v: named %+v", scale, left, got)
			}
		}
	}
}

// The second look and the lone-entry re-verification run in parallel on
// shared prepared templates; they equal the serial reference exactly.
func TestAvatarSecondLookEqualsLegacySerialScan(t *testing.T) {
	c, err := loadEmbeddedAvatarCatalog()
	if err != nil {
		t.Fatal(err)
	}
	rescued := 0
	for _, tc := range hudPortraitCases {
		for _, scale := range hudPortraitScales {
			factor := tc.factors[len(tc.factors)/2]
			for _, left := range []bool{true, false} {
				img, window := hudPortraitFrame(t, c, tc.id, scale, factor, left)
				label := fmt.Sprintf("%s scale=%.3f left=%v", tc.id, scale, left)
				want := legacyMatch(c, img, window, scale)
				got := c.Match(img, window, scale)
				sameAvatarMatch(t, label+" full", got, want)
				if want.ID != "" && legacyScan(c, img, window, scale).ID == "" {
					rescued++
				}
				view := img.SubImage(window.Intersect(img.Bounds())).(*image.RGBA)
				c.mu.Lock()
				lone := []*avatarEntry{c.byID[tc.id]}
				wantLone := legacyMatchEntries(c, view, nil, scale, lone)
				c.mu.Unlock()
				sameAvatarMatch(t, label+" lone", c.matchEntries(view, nil, scale, lone), wantLone)
			}
		}
	}
	t.Logf("%d fixtures took the second look", rescued)
	if rescued == 0 {
		t.Fatal("no fixture took the second look; equivalence is vacuous")
	}
}
