// Package draw 提供 RGBA 图像上的标注绘制（菱形框、点阵编号、直线）。
// 仅用于可视化显示层，与豆位识别算法完全解耦——识别永远基于原始截图，
// 标注绘制在副本上，绝不回写原始图像。
package draw

import (
	"image"
	"image/color"
)

// Diamond 以 (cx,cy) 为中心画菱形边框，w/h 为菱形宽高。
// 尺寸与 detect.InBeadDiamond 采样区域一致（纯显示，不影响识别）。
func Diamond(img *image.RGBA, cx, cy, w, h int, c color.RGBA) {
	Line(img, cx, cy-h/2, cx+w/2, cy, c)
	Line(img, cx+w/2, cy, cx, cy+h/2, c)
	Line(img, cx, cy+h/2, cx-w/2, cy, c)
	Line(img, cx-w/2, cy, cx, cy-h/2, c)
}

// Label 用 3x5 点阵字符绘制短标签（如 "L1"、"R3"），scale 为放大倍数。
func Label(img *image.RGBA, x, y int, s string, c color.RGBA, scale int) {
	if scale <= 0 {
		scale = 1
	}
	cx := x
	for _, ch := range s {
		glyph, ok := glyphs[ch]
		if ok {
			for r := 0; r < 5; r++ {
				for cc := 0; cc < 3; cc++ {
					if glyph[r][cc] == 0 {
						continue
					}
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							Set(img, cx+cc*scale+dx, y+r*scale+dy, c)
						}
					}
				}
			}
		}
		cx += 4 * scale // 字距（未知字符/空格也占位）
	}
}

// glyphs 3x5 点阵字符（L、R、1-4，可扩展）。
var glyphs = map[rune][5][3]byte{
	'L': {{1, 0, 0}, {1, 0, 0}, {1, 0, 0}, {1, 0, 0}, {1, 1, 1}},
	'R': {{1, 1, 1}, {1, 0, 1}, {1, 1, 1}, {1, 1, 0}, {1, 0, 1}},
	'1': {{0, 1, 0}, {1, 1, 0}, {0, 1, 0}, {0, 1, 0}, {1, 1, 1}},
	'2': {{1, 1, 1}, {0, 0, 1}, {1, 1, 1}, {1, 0, 0}, {1, 1, 1}},
	'3': {{1, 1, 1}, {0, 0, 1}, {1, 1, 1}, {0, 0, 1}, {1, 1, 1}},
	'4': {{1, 0, 1}, {1, 0, 1}, {1, 1, 1}, {0, 0, 1}, {0, 0, 1}},
}

// BeadMarker 画豆位标注：红色菱形框 + 白色编号（纯显示层，不影响识别）。
func BeadMarker(img *image.RGBA, x, y int, label string) {
	BeadMarkerState(img, x, y, label, color.RGBA{R: 255, G: 40, B: 40, A: 255})
}

// BeadMarkerState 用指定颜色画菱形框 + 编号。
// 框只圈豆心；编号画在豆子下方，避开血条/能量条。
func BeadMarkerState(img *image.RGBA, x, y int, label string, c color.RGBA) {
	Diamond(img, x, y, 9, 12, c)
	lx := x - 10
	ly := y + 22
	if len(label) > 0 && label[0] == 'R' {
		lx = x + 4
	}
	Label(img, lx, ly, label, color.RGBA{R: 255, G: 255, B: 255, A: 255}, 2)
}

// FillDiamond 实心填充菱形（用于状态面板的豆灯）。
func FillDiamond(img *image.RGBA, cx, cy, w, h int, c color.RGBA) {
	for dy := -h / 2; dy <= h/2; dy++ {
		for dx := -w / 2; dx <= w/2; dx++ {
			if InDiamond(w, h, dx, dy) {
				Set(img, cx+dx, cy+dy, c)
			}
		}
	}
}

// InDiamond 判断相对偏移是否落在以原点为中心的菱形内（|x|/w + |y|/h <= 0.5）。
func InDiamond(w, h, x, y int) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	return abs(x)*2*h+abs(y)*2*w <= w*h
}
func Set(img *image.RGBA, x, y int, c color.RGBA) {
	b := img.Bounds()
	if x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y {
		return
	}
	img.SetRGBA(x, y, c)
}

// CopyRGBA 深拷贝 RGBA 图像（标注永远画在副本上，不污染原始截图）。
func CopyRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}

// Line Bresenham 直线。
func Line(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx := 1
	if x0 > x1 {
		sx = -1
	}
	sy := 1
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		Set(img, x0, y0, c)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
