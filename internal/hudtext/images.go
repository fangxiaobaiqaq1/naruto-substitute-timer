package hudtext

import (
	"crypto/sha256"
	"image"
	"image/color"
	"math"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/match"
	"narutotimer/internal/ocr"
)

const variants = 3

type strip struct {
	img       *image.RGBA
	signature [32]byte
	white     int
}
type row struct {
	side, variant int
	rect          image.Rectangle
}
type sheet struct {
	image *image.Gray
	rows  []row
}

func nameRegions(img *image.RGBA, cfg config.LayoutConfig, profile string) ([2]image.Rectangle, bool) {
	var out [2]image.Rectangle
	mode, err := detect.ParseMode(cfg.ContentMode)
	if err != nil {
		return out, false
	}
	ca, ok := detect.ResolveContentArea(img, mode, cfg.ReferenceWidth, cfg.ReferenceHeight, cfg.AutoAspectTolerance)
	if !ok {
		return out, false
	}
	p := cfg.Profile(profile)
	scale := float64(ca.W) / 960
	for i, side := range []config.SideLayout{p.Left, p.Right} {
		if len(side.NominalCenters) == 0 {
			return out, false
		}
		first := side.NominalCenters[0]
		center := image.Pt(ca.X+int(math.Round(first.X*float64(ca.W))), ca.Y+int(math.Round(first.Y*float64(ca.H))))
		x0, x1 := -4., 330.
		if i == 1 {
			x0, x1 = -330., 4.
		}
		out[i] = image.Rect(center.X+int(math.Round(x0*scale)), ca.Y+int(math.Round(.020*float64(ca.H))), center.X+int(math.Round(x1*scale)), ca.Y+int(math.Round(.073*float64(ca.H)))).Intersect(img.Bounds())
		if out[i].Dx() < 80 || out[i].Dy() < 10 {
			return out, false
		}
	}
	return out, true
}

// Conservative fallback before OCR supplies usable word coordinates. This
// broad strip may also contain outlined scenery, so established fields use
// their own live glyph evidence rather than letting any strip pixel erase them.
func lettering(img *image.RGBA, roi image.Rectangle) ([32]byte, int) {
	bits := make([]byte, (roi.Dx()*roi.Dy()+7)/8)
	count := 0
	for y := roi.Min.Y; y < roi.Max.Y; y++ {
		for x := roi.Min.X; x < roi.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if min(c.R, c.G, c.B) < 180 || int(max(c.R, c.G, c.B))-int(min(c.R, c.G, c.B)) > 65 {
				continue
			}
			outlined := false
			for _, p := range []image.Point{{x - 2, y}, {x + 2, y}, {x, y - 2}, {x, y + 2}} {
				if p.In(img.Bounds()) {
					n := img.RGBAAt(p.X, p.Y)
					if max(n.R, n.G, n.B) < 90 {
						outlined = true
						break
					}
				}
			}
			if !outlined {
				continue
			}
			i := (y-roi.Min.Y)*roi.Dx() + x - roi.Min.X
			bits[i/8] |= 1 << uint(i%8)
			count++
		}
	}
	return sha256.Sum256(bits), count
}

// buildSheet runs only in the OCR worker. It gives the engine three independent
// visual treatments per side: normal luma and inverse luma at two sizes.
// Integer scaling preserves letter shapes better than an arbitrary target width.
// Coordinates, not synthetic row labels, associate OCR lines with each crop.
func buildSheet(strips [2]strip) sheet {
	width := 1
	var parts []*image.Gray
	var rows []row
	y := 0
	for side, s := range strips {
		if s.img == nil {
			continue
		}
		base := match.ToGray(s.img)

		for v := 0; v < variants; v++ {
			src := image.NewGray(base.Bounds())
			for py := 0; py < base.Bounds().Dy(); py++ {
				for px := 0; px < base.Bounds().Dx(); px++ {
					value := base.GrayAt(px, py).Y
					if v >= 1 {
						value = 255 - value
					}

					src.SetGray(px, py, color.Gray{Y: value})
				}
			}
			factor := 2
			if v == 2 {
				factor = 4
			}
			w := min(2200, src.Bounds().Dx()*factor)
			h := max(16, int(math.Round(float64(src.Bounds().Dy())*float64(w)/float64(src.Bounds().Dx()))))
			dst := match.ScaleGray(src, w, h)
			width = max(width, w+24)
			parts = append(parts, dst)
			rows = append(rows, row{side: side, variant: v, rect: image.Rect(12, y+12, 12+w, y+12+h)})
			y += h + 32
		}
	}
	img := image.NewGray(image.Rect(0, 0, width, y))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for i, part := range parts {
		bounds := rows[i].rect
		for py := 0; py < bounds.Dy(); py++ {
			copy(img.Pix[(bounds.Min.Y+py)*img.Stride+bounds.Min.X:], part.Pix[py*part.Stride:(py+1)*part.Stride])
		}
	}
	return sheet{image: img, rows: rows}
}

func (s sheet) texts(lines []ocr.Line) [2][variants]string {
	var out [2][variants]string
	for _, row := range s.rows {
		for _, w := range wordsForRow(lines, row.rect) {
			if out[row.side][row.variant] != "" {
				out[row.side][row.variant] += " "
			}
			out[row.side][row.variant] += w.Text
		}
	}
	return out
}
