package hudtext

import (
	"image"
	"testing"

	"narutotimer/internal/ocr"
)

func TestWordGeometryRemovesOnlySmallDetachedMarks(t *testing.T) {
	word := func(text string, x, w float64) ocr.Word {
		return ocr.Word{Text: text, X: x, Y: 5, Width: w, Height: 20}
	}
	for _, tc := range []struct {
		name  string
		words []ocr.Word
		want  string
	}{
		{"detached-prefix", []ocr.Word{word("0", 5, 8), word("名字", 100, 30)}, "名字"},
		{"detached-suffix", []ocr.Word{word("名字", 100, 30), word("0", 200, 8)}, "名字"},
		{"close-prefix", []ocr.Word{word("姓", 90, 8), word("名字", 100, 30)}, "姓名字"},
		{"substantial-groups", []ocr.Word{word("名字", 10, 30), word("版本账号", 120, 60)}, "名字版本账号"},
		{"parentheses", []ocr.Word{word("名字", 20, 30), word("(账号)", 52, 40)}, "名字(账号)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ""
			for _, w := range wordsForRow([]ocr.Line{{Words: tc.words}}, image.Rect(0, 0, 400, 30)) {
				got += w.Text
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
