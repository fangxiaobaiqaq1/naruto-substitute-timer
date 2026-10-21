package detect

import (
	"image"
	"testing"
)

func TestMapHUDKeepsPhysicalSizeOnNativeWideSurface(t *testing.T) {
	wide := ContentArea{W: 3440, H: 1440}
	left := wide.MapHUDPixel(155, 101, "left", HUDReferenceWidth, HUDReferenceHeight)
	right := wide.MapHUDPixel(1391, 101, "right", HUDReferenceWidth, HUDReferenceHeight)
	if left != image.Pt(248, 162) {
		t.Fatalf("left HUD point = %v, want (248,162)", left)
	}
	if right != image.Pt(3106, 162) {
		t.Fatalf("right HUD point = %v, want (3106,162)", right)
	}
	// Increasing the width only moves a right-anchored point; the left HUD
	// remains at the same physical x and both points keep the same y/scale.
	wider := ContentArea{W: 4096, H: 1440}
	if got := wider.MapHUDPixel(155, 101, "left", HUDReferenceWidth, HUDReferenceHeight); got.X != left.X {
		t.Fatalf("left point changed with extra center width: %v vs %v", got, left)
	}
	if got := wider.MapHUDPixel(1391, 101, "right", HUDReferenceWidth, HUDReferenceHeight); got.X != right.X+656 || got.Y != right.Y {
		t.Fatalf("right anchor did not follow surface edge: %v vs %v", got, right)
	}
}

func TestMapHUDMatchesNormalizedCoordinatesAtSixteenNine(t *testing.T) {
	for _, ca := range []ContentArea{{W: 1280, H: 720}, {W: 1600, H: 900}, {W: 1920, H: 1080}} {
		left := ca.MapHUDNormalized(155.0/HUDReferenceWidth, 101.0/HUDReferenceHeight, "left", HUDReferenceWidth, HUDReferenceHeight)
		right := ca.MapHUDNormalized(1391.0/HUDReferenceWidth, 101.0/HUDReferenceHeight, "right", HUDReferenceWidth, HUDReferenceHeight)
		wantLeft := image.Pt(int(float64(ca.W)*155/1600+.5), int(float64(ca.H)*101/900+.5))
		wantRight := image.Pt(int(float64(ca.W)*1391/1600+.5), int(float64(ca.H)*101/900+.5))
		if left != wantLeft || right != wantRight {
			t.Fatalf("%dx%d: got left=%v right=%v, want left=%v right=%v", ca.W, ca.H, left, right, wantLeft, wantRight)
		}
	}
}

func TestMapHUDHonorsLetterboxOrigin(t *testing.T) {
	ca := ContentArea{X: 0, Y: 40, W: 1280, H: 720}
	p := ca.MapHUDPixel(155, 101, "left", HUDReferenceWidth, HUDReferenceHeight)
	if p != image.Pt(124, 121) {
		t.Fatalf("letterboxed HUD point = %v, want (124,121)", p)
	}
}

func TestHUDScaleFitsTabletsWithoutMovingTopAnchor(t *testing.T) {
	for _, size := range []image.Point{{1516, 948}, {2880, 1800}, {2800, 1752}, {1920, 1200}} {
		ca := ContentArea{X: 23, Y: 31, W: size.X, H: size.Y}
		wide := ContentArea{X: ca.X, Y: ca.Y, W: ca.W, H: ca.W * 9 / 16}
		for _, side := range []string{"left", "right"} {
			p := ca.MapHUDPixel(172, 103, side, HUDReferenceWidth, HUDReferenceHeight)
			q := wide.MapHUDPixel(172, 103, side, HUDReferenceWidth, HUDReferenceHeight)
			if d := p.Sub(q); d.X < -1 || d.X > 1 || d.Y < -1 || d.Y > 1 {
				t.Fatalf("%v %s: tablet=%v 16:9=%v; HUD must fit width and stay at the top", size, side, p, q)
			}
		}
		if got, want := ca.HUDScale(1600, 900), float64(ca.W)/1600; got != want {
			t.Fatalf("%v scale=%v, want %v", size, got, want)
		}
	}
	for _, ca := range []ContentArea{{}, {W: 1600}, {H: 900}, {W: -1, H: 900}} {
		if ca.HUDScale(1600, 900) != 0 {
			t.Fatalf("invalid area has nonzero HUD scale: %+v", ca)
		}
	}
	ca := ContentArea{W: 1600, H: 900}
	if ca.HUDScale(0, 900) != 0 || ca.HUDScale(1600, 0) != 0 {
		t.Fatal("invalid reference has nonzero HUD scale")
	}
}

func TestLegacyLayoutUsesHUDAnchors(t *testing.T) {
	pos := Layout(3440, 1440, ModeStretch, []Bead{
		{Side: "left", LX: 155, LY: 101},
		{Side: "right", LX: 1391, LY: 101},
	})
	if pos[0].X != 207 || pos[1].X != 2735 || pos[0].Y != 135 || pos[1].Y != 135 {
		t.Fatalf("legacy layout = %+v", pos)
	}
}
