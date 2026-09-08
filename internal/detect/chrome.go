package detect

import "image"

// GuessTopChrome 估计模拟器标题栏/标签栏占用的顶部像素。
// 客户区常把 MuMu 顶栏算进去，游戏画面整体下移，模板和豆位都会偏。
func GuessTopChrome(img *image.RGBA) int {
	if img == nil {
		return 0
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 400 || h < 250 {
		return 0
	}
	limit := h / 5
	if limit > 72 {
		limit = 72
	}
	chrome := 0
	for y := 0; y < limit; y++ {
		var colorful, n int
		for x := 0; x < w; x += 4 {
			c := img.RGBAAt(b.Min.X+x, b.Min.Y+y)
			n++
			if isColorful(c.R, c.G, c.B) {
				colorful++
			}
		}
		if n == 0 {
			break
		}
		if float64(colorful)/float64(n) > 0.12 {
			break
		}
		chrome = y + 1
	}
	if chrome < 18 || chrome > 64 {
		return 0
	}
	return chrome
}

// StripChrome 裁掉顶部模拟器栏，没有则原样返回。
func StripChrome(img *image.RGBA) *image.RGBA {
	top := GuessTopChrome(img)
	if top == 0 || img == nil {
		return img
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()-top))
	for y := 0; y < out.Bounds().Dy(); y++ {
		for x := 0; x < out.Bounds().Dx(); x++ {
			out.SetRGBA(x, y, img.RGBAAt(b.Min.X+x, b.Min.Y+top+y))
		}
	}
	return out
}

func isColorful(r, g, b uint8) bool {
	maxc, minc := r, r
	if g > maxc {
		maxc = g
	}
	if b > maxc {
		maxc = b
	}
	if g < minc {
		minc = g
	}
	if b < minc {
		minc = b
	}
	return int(maxc)-int(minc) > 40 && maxc > 80
}
