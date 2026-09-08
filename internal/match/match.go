// Package match 提供与游戏无关的模板匹配器。
// 第一实现是灰度 NCC；以后可换哈希 / 其它 Matcher，场景门闩不用改。
package match

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
)

// Score 是一次匹配的峰值结果。
type Score struct {
	Value    float64     // NCC，截到 [0,1] 后再和阈值比
	Peak     image.Point // 搜索框内峰值左上角（截图像素）
	Template string
}

// Query 描述一次匹配。ROI 已是截图像素；Template / Mask 为灰度。
type Query struct {
	Image    *image.RGBA
	Gray     *image.Gray // 可选，同一截图的灰度；尺寸须与 Image 一致，原点可不同
	ROI      image.Rectangle
	Template *image.Gray
	Mask     *image.Gray  // 可选，0 = 忽略
	Prepared *PreparedNCC // Optional immutable template+mask snapshot; authoritative when set.
}

// Matcher 把一块图和一张模板比出分数。
type Matcher interface {
	Match(q Query) (Score, error)
}

// NCC 是归一化互相关实现。
type NCC struct{}

// Match 在 ROI 内滑动模板，返回最高 NCC。
func (NCC) Match(q Query) (Score, error) {
	if q.Image == nil || (q.Template == nil && q.Prepared == nil) {
		return Score{}, fmt.Errorf("match: image and template are required")
	}
	prepared := q.Prepared
	if prepared == nil {
		prepared = PrepareNCC(q.Template, q.Mask)
	}
	if prepared == nil {
		return Score{}, fmt.Errorf("match: empty template")
	}
	tb := prepared.rect
	tw, th := tb.Dx(), tb.Dy()
	if tw < 1 || th < 1 {
		return Score{}, fmt.Errorf("match: empty template")
	}
	ib := q.Image.Bounds()
	if q.Gray != nil && q.Gray.Bounds().Size() != ib.Size() {
		return Score{}, fmt.Errorf("match: gray image size %v does not match image size %v", q.Gray.Bounds().Size(), ib.Size())
	}
	roi := q.ROI.Intersect(ib)
	if roi.Empty() {
		return Score{Template: tb.String()}, nil
	}
	if roi.Dx() < tw || roi.Dy() < th {
		return Score{Peak: roi.Min}, nil
	}

	// 小模板 1px 错位就会打穿；大厅底栏那种大图才粗扫。
	minSide := tw
	if th < minSide {
		minSide = th
	}
	step := 1
	if minSide >= 48 {
		step = minSide / 8
		if step < 2 {
			step = 2
		}
	}

	gray := q.Gray
	if gray == nil {
		gray = ToGray(q.Image)
	}
	// ToGray(RGBA) 从零开始，而调用方也可传带原点和 stride 的 Gray 子图。
	// nccAt 使用相对于 Gray.Bounds().Min 的偏移，输出仍是截图坐标。
	origin := ib.Min
	best := Score{Peak: roi.Min}
	scan := func(x0, y0, x1, y1, st int) {
		if st < 1 {
			st = 1
		}
		if x0 < roi.Min.X {
			x0 = roi.Min.X
		}
		if y0 < roi.Min.Y {
			y0 = roi.Min.Y
		}
		if x1 > roi.Max.X-tw {
			x1 = roi.Max.X - tw
		}
		if y1 > roi.Max.Y-th {
			y1 = roi.Max.Y - th
		}
		for y := y0; y <= y1; y += st {
			for x := x0; x <= x1; x += st {
				v := prepared.at(gray, x-origin.X, y-origin.Y)
				if v > best.Value {
					best.Value = v
					best.Peak = image.Pt(x, y)
				}
			}
		}
	}
	scan(roi.Min.X, roi.Min.Y, roi.Max.X-tw, roi.Max.Y-th, step)
	if step > 1 {
		scan(best.Peak.X-step, best.Peak.Y-step, best.Peak.X+step, best.Peak.Y+step, 1)
	}
	return best, nil
}

// ToGray 把任意图转成灰度。
func ToGray(src image.Image) *image.Gray {
	if src == nil {
		return image.NewGray(image.Rect(0, 0, 0, 0))
	}
	if g, ok := src.(*image.Gray); ok {
		out := image.NewGray(g.Bounds())
		draw.Draw(out, out.Bounds(), g, g.Bounds().Min, draw.Src)
		return out
	}
	b := src.Bounds()
	out := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	// Capture frames are RGBA. Accessing them through image.Image.At boxes a
	// color for every pixel; the direct path preserves the same integer luma
	// calculation and also handles subimages with a padded stride.
	if rgba, ok := src.(*image.RGBA); ok {
		for y := 0; y < b.Dy(); y++ {
			srcRow := rgba.Pix[y*rgba.Stride : y*rgba.Stride+b.Dx()*4]
			dstRow := out.Pix[y*out.Stride : y*out.Stride+b.Dx()]
			for x := range dstRow {
				i := x * 4
				dstRow[x] = uint8((299*uint32(srcRow[i]) + 587*uint32(srcRow[i+1]) + 114*uint32(srcRow[i+2]) + 500) / 1000)
			}
		}
		return out
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := src.At(x, y).RGBA()
			y8 := uint8((299*uint32(r>>8) + 587*uint32(g>>8) + 114*uint32(bl>>8) + 500) / 1000)
			out.SetGray(x-b.Min.X, y-b.Min.Y, color.Gray{Y: y8})
		}
	}
	return out
}

// ScaleGray 双线性缩放到指定尺寸。
func ScaleGray(src *image.Gray, w, h int) *image.Gray {
	if src == nil {
		return image.NewGray(image.Rect(0, 0, 0, 0))
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dst := image.NewGray(image.Rect(0, 0, w, h))
	if sw < 1 || sh < 1 {
		return dst
	}
	if sw == w && sh == h {
		draw.Draw(dst, dst.Bounds(), src, sb.Min, draw.Src)
		return dst
	}
	for y := 0; y < h; y++ {
		fy := (float64(y)+0.5)*float64(sh)/float64(h) - 0.5
		if fy < 0 {
			fy = 0
		}
		y0 := int(fy)
		y1 := y0 + 1
		if y0 >= sh {
			y0 = sh - 1
		}
		if y1 >= sh {
			y1 = sh - 1
		}
		wy := fy - float64(y0)
		for x := 0; x < w; x++ {
			fx := (float64(x)+0.5)*float64(sw)/float64(w) - 0.5
			if fx < 0 {
				fx = 0
			}
			x0 := int(fx)
			x1 := x0 + 1
			if x0 >= sw {
				x0 = sw - 1
			}
			if x1 >= sw {
				x1 = sw - 1
			}
			wx := fx - float64(x0)
			v00 := float64(src.GrayAt(sb.Min.X+x0, sb.Min.Y+y0).Y)
			v10 := float64(src.GrayAt(sb.Min.X+x1, sb.Min.Y+y0).Y)
			v01 := float64(src.GrayAt(sb.Min.X+x0, sb.Min.Y+y1).Y)
			v11 := float64(src.GrayAt(sb.Min.X+x1, sb.Min.Y+y1).Y)
			v := (1-wx)*(1-wy)*v00 + wx*(1-wy)*v10 + (1-wx)*wy*v01 + wx*wy*v11
			dst.SetGray(x, y, color.Gray{Y: uint8(v + 0.5)})
		}
	}
	return dst
}

// CropRGBA 裁一块并复制成独立 RGBA。
func CropRGBA(src *image.RGBA, r image.Rectangle) *image.RGBA {
	if src == nil {
		return nil
	}
	r = r.Intersect(src.Bounds())
	if r.Empty() {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), src, r.Min, draw.Src)
	return out
}
