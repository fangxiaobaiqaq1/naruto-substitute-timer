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
	step := coarseStep(tw, th)

	gray := q.Gray
	if gray == nil {
		gray = ToGray(q.Image)
	}
	// ToGray(RGBA) 从零开始，而调用方也可传带原点和 stride 的 Gray 子图。
	// nccAt 使用相对于 Gray.Bounds().Min 的偏移，输出仍是截图坐标。
	origin := ib.Min
	// Every footprint scanned lies inside the ROI. The SIMD kernel reads the
	// gray pixels directly (no bounds checks), so it is used only when the
	// ROI's last pixel provably lies inside gray.Pix; otherwise the portable
	// path keeps Go's bounds checks. Both give bit-identical scores.
	kernel := nccKernel != nil && kernelFits(gray, roi.Sub(origin))
	var pre *grayPrefix
	if !kernel {
		// The prefix sums cover exactly the ROI; they are local to this call,
		// hence single-goroutine.
		pre = newGrayPrefix(gray, roi.Min.X-origin.X, roi.Min.Y-origin.Y, roi.Dx(), roi.Dy())
		defer pre.release()
	}
	best := Score{Peak: roi.Min}
	// Grid points already scored by an earlier pass of this call are not
	// scored again: best.Value can only have grown since, so an identical
	// re-score could never pass the strict '>' below. The result is the same,
	// with fewer NCC evaluations in the refinement windows.
	var passes [2]scanPass
	npass := 0
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
			var rows [2]bool
			anyRow := false
			for i, p := range passes[:npass] {
				rows[i] = p.onRow(y)
				anyRow = anyRow || rows[i]
			}
			for x := x0; x <= x1; x += st {
				if anyRow && ((rows[0] && passes[0].onCol(x)) || (rows[1] && passes[1].onCol(x))) {
					continue
				}
				var v float64
				if kernel {
					v = prepared.atKernel(gray.Pix, gray.Stride, (y-origin.Y)*gray.Stride+x-origin.X)
				} else {
					v = prepared.at(gray, pre, x-origin.X, y-origin.Y)
				}
				if v > best.Value {
					best.Value = v
					best.Peak = image.Pt(x, y)
				}
			}
		}
		if npass < len(passes) {
			passes[npass] = scanPass{x0, y0, x1, y1, st}
			npass++
		}
	}
	scan(roi.Min.X, roi.Min.Y, roi.Max.X-tw, roi.Max.Y-th, step)
	if step > 1 {
		// Refine the coarse peak. For large templates the old exhaustive
		// (2*step+1)^2 window was the dominant cost of the whole scene gate; a
		// medium grid narrows the window first, then 1-px search finishes.
		if mid := refineStep(step); mid > 1 {
			scan(best.Peak.X-step, best.Peak.Y-step, best.Peak.X+step, best.Peak.Y+step, mid)
			scan(best.Peak.X-mid, best.Peak.Y-mid, best.Peak.X+mid, best.Peak.Y+mid, 1)
		} else {
			scan(best.Peak.X-step, best.Peak.Y-step, best.Peak.X+step, best.Peak.Y+step, 1)
		}
	}
	return best, nil
}

// scanPass is the grid one scan visited: x0+i*st <= x1, y0+j*st <= y1.
type scanPass struct{ x0, y0, x1, y1, st int }

func (p scanPass) onRow(y int) bool { return y >= p.y0 && y <= p.y1 && (y-p.y0)%p.st == 0 }
func (p scanPass) onCol(x int) bool { return x >= p.x0 && x <= p.x1 && (x-p.x0)%p.st == 0 }

// kernelFits reports whether rel (ROI in gray offsets, i.e. relative to the
// image origin) lies inside gray.Pix, so that every template footprint within
// it can be read without bounds checks.
func kernelFits(gray *image.Gray, rel image.Rectangle) bool {
	if rel.Empty() || rel.Min.X < 0 || rel.Min.Y < 0 || gray.Stride < rel.Max.X {
		return false
	}
	last := (rel.Max.Y-1)*gray.Stride + rel.Max.X - 1
	return last < len(gray.Pix)
}

// coarseStep is the sparse grid for large templates; small templates cannot
// tolerate a single pixel of misalignment and are scanned exhaustively.
func coarseStep(tw, th int) int {
	minSide := min(tw, th)
	if minSide < 48 {
		return 1
	}
	return max(2, minSide/8)
}

// refineStep is the medium grid used between the coarse scan and the final
// 1-px window. It stays within a quarter of the coarse step so the true peak,
// whose correlation lobe is a few pixels wide for HUD text, is not skipped.
func refineStep(step int) int {
	return step / 4
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
		// RGBA subimages keep their parent Pix and Stride. Include Rect.Min so
		// a cropped HUD ROI is converted from its own current pixels, not from
		// the top-left of the parent frame.
		xoff, yoff := b.Min.X-rgba.Rect.Min.X, b.Min.Y-rgba.Rect.Min.Y
		w, h := b.Dx(), b.Dy()
		done := 0 // leading columns converted by the SIMD rows
		if w4 := w &^ 3; grayRows != nil && w4 > 0 && h > 0 && xoff >= 0 && yoff >= 0 {
			first := yoff*rgba.Stride + xoff*4
			if last := first + (h-1)*rgba.Stride + w4*4; last <= len(rgba.Pix) && rgba.Stride >= w4*4 {
				grayRows(&out.Pix[0], out.Stride, &rgba.Pix[first], rgba.Stride, w4, h)
				done = w4
			}
		}
		for y := 0; y < h; y++ {
			start := (y+yoff)*rgba.Stride + xoff*4
			srcRow := rgba.Pix[start : start+w*4]
			dstRow := out.Pix[y*out.Stride : y*out.Stride+w]
			for x := done; x < len(dstRow); x++ {
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
