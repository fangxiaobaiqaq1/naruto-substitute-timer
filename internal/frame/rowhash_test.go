package frame

import (
	"image"
	"math/rand/v2"
	"testing"
	"time"
)

var at0 = time.Unix(1700000000, 0)

// A capture client's per-row hashes fold into exactly frameFingerprint, for
// packed and padded strides, empty images and every geometry.
func TestFrameFingerprintRowsEqualsFrameFingerprint(t *testing.T) {
	r := rand.New(rand.NewPCG(8, 9))
	for _, rect := range []image.Rectangle{image.Rect(0, 0, 0, 0), image.Rect(0, 0, 1, 1), image.Rect(0, 0, 7, 3), image.Rect(0, 0, 1920, 4), image.Rect(3, 5, 40, 30)} {
		for _, pad := range []int{0, 12} {
			stride := rect.Dx()*4 + pad
			img := &image.RGBA{Pix: make([]byte, stride*rect.Dy()), Stride: stride, Rect: rect}
			for i := range img.Pix {
				img.Pix[i] = uint8(r.IntN(256))
			}
			var rows []uint64
			for y := 0; y < rect.Dy(); y++ {
				rows = append(rows, fingerprintRow(img.Pix[y*stride:y*stride+rect.Dx()*4]))
			}
			for _, method := range []string{"sdk", "MuMu 实例 0"} {
				if got, want := frameFingerprintRows(rect, method, rows), frameFingerprint(img, method); got != want {
					t.Fatalf("%v pad=%d %s: rows fold %x, fingerprint %x", rect, pad, method, got, want)
				}
			}
		}
	}
}

// analyzeFingerprinted with the client's fold behaves exactly like analyze.
func TestAnalyzeFingerprintedEqualsAnalyze(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 800, 450))
	var rows []uint64
	for y := 0; y < 450; y++ {
		rows = append(rows, fingerprintRow(img.Pix[y*img.Stride:(y+1)*img.Stride]))
	}
	var a, b dedupedAnalysis
	eng := &calibratingEngine{}
	fa := a.analyze(img, eng, 0, at0, at0, "sdk")
	fb := b.analyzeFingerprinted(img, frameFingerprintRows(img.Rect, "sdk", rows), eng, 0, at0, at0, "sdk")
	if fa.fingerprint != fb.fingerprint || !fa.fingerprinted || !fb.fingerprinted || a.fingerprint != b.fingerprint {
		t.Fatalf("fingerprints %x / %x", fa.fingerprint, fb.fingerprint)
	}
}
