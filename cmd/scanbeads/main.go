package main

import (
	"fmt"
	"image"
	"image/png"
	"os"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
)

func hsv(r, g, b int) (h, s, v float64) {
	rf := float64(r) / 255
	gf := float64(g) / 255
	bf := float64(b) / 255
	max := rf
	if gf > max {
		max = gf
	}
	if bf > max {
		max = bf
	}
	min := rf
	if gf < min {
		min = gf
	}
	if bf < min {
		min = bf
	}
	v = max
	d := max - min
	if max == 0 {
		s = 0
	} else {
		s = d / max
	}
	switch {
	case d < 1e-9:
		h = 0
	case max == rf:
		h = (gf - bf) / d
		if h < 0 {
			h += 6
		}
	case max == gf:
		h = (bf-rf)/d + 2
	default:
		h = (rf-gf)/d + 4
	}
	h /= 6
	return
}

func inRange(h, s, v float64, r config.HSVRange) bool {
	return h >= r.HMin && h <= r.HMax && s >= r.SMin && s <= r.SMax && v >= r.VMin && v <= r.VMax
}

func classifyPix(r, g, b int, cfg config.VisionConfig) string {
	h, s, v := hsv(r, g, b)
	for _, rg := range cfg.Gold {
		if inRange(h, s, v, rg) {
			return "gold"
		}
	}
	for _, rg := range cfg.Light {
		if inRange(h, s, v, rg) {
			return "light"
		}
	}
	for _, rg := range cfg.Dark {
		if inRange(h, s, v, rg) {
			return "dark"
		}
	}
	return "other"
}

func main() {
	path := `debug/dump-20260814-002318-raw.png`
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		panic(err)
	}
	b := src.Bounds()
	img := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			img.Set(x, y, src.At(x, y))
		}
	}
	w, h := b.Dx(), b.Dy()
	fmt.Printf("image %dx%d\n", w, h)
	fmt.Printf("top-left RGB %v\n", img.RGBAAt(10, 10))
	fmt.Printf("below chrome RGB %v\n", img.RGBAAt(10, 40))
	ca := detect.ComputeContentArea(w, h, detect.ModeAuto)
	fmt.Printf("content %+v\n", ca)
	pos := detect.Layout(w, h, detect.ModeAuto, detect.DefaultBeads())
	cfg := config.Default().Vision
	sw, sh := 9, 12
	for _, p := range pos {
		var light, dark, gold, other int
		var sr, sg, sb, n int
		for dy := -sh / 2; dy <= sh/2; dy++ {
			for dx := -sw / 2; dx <= sw/2; dx++ {
				if !detect.InBeadDiamond(0, 0, sw, sh, dx, dy) {
					continue
				}
				x, y := p.X+dx, p.Y+dy
				if x < 0 || y < 0 || x >= w || y >= h {
					continue
				}
				c := img.RGBAAt(x, y)
				r, g, b := int(c.R), int(c.G), int(c.B)
				sr += r
				sg += g
				sb += b
				n++
				switch classifyPix(r, g, b, cfg) {
				case "gold":
					gold++
				case "light":
					light++
				case "dark":
					dark++
				default:
					other++
				}
			}
		}
		if n == 0 {
			n = 1
		}
		fmt.Printf("%s%d (%3d,%3d) avg(%3d,%3d,%3d) L=%d D=%d G=%d O=%d charged=%.2f\n",
			p.Side, p.Idx+1, p.X, p.Y, sr/n, sg/n, sb/n, light, dark, gold, other,
			float64(light+gold)/float64(n))
	}
}
