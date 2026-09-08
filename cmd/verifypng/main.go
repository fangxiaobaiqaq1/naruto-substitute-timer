// 临时工具：对已有 PNG 执行完整豆位检测流程（画面状态 + 采样 + 递增修正）
package main

import (
	"fmt"
	"image"
	"image/png"
	"os"

	"narutotimer/internal/detect"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法: verifypng <png> [w] [h] [mode]")
		os.Exit(1)
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Println("打开失败:", err)
		os.Exit(1)
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		fmt.Println("解码失败:", err)
		os.Exit(1)
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	var mode detect.ContentMode = detect.ModeAuto
	if len(os.Args) >= 5 {
		mode, _ = detect.ParseMode(os.Args[4])
	}
	rgba := image.NewRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			rgba.Set(x, y, img.At(x, y))
		}
	}
	fmt.Printf("图片: %s (%dx%d) mode=%v\n", os.Args[1], w, h, mode)
	scr := detect.ClassifyScreen(rgba, mode)
	fmt.Printf("画面状态: %s\n", scr)
	if scr != detect.ScreenFighting {
		return
	}
	positions := detect.Layout(w, h, mode, detect.DefaultBeads())
	lastSide := ""
	for _, p := range positions {
		if p.Side != lastSide {
			fmt.Printf("\n%s 侧 (镜像: 右侧豆1在最右):\n", mapSide(p.Side))
			lastSide = p.Side
		}
		state, r, g, b := vote(rgba, p.X, p.Y)
		fmt.Printf("  豆%d (%3d,%3d)  RGB(%3d,%3d,%3d)  → %s\n", p.Idx+1, p.X, p.Y, r, g, b, state)
	}
	fmt.Println()
	for _, side := range []string{"left", "right"} {
		var states []detect.BeadState
		for _, p := range positions {
			if p.Side == side {
				s, _, _, _ := vote(rgba, p.X, p.Y)
				states = append(states, s)
			}
		}
		fixed := detect.ApplyIncrementRule(states)
		fmt.Printf("%s 侧递增修正后: %v → 亮 %d 颗\n", mapSide(side), fixed, detect.CountLight(fixed))
	}
}

func mapSide(s string) string {
	if s == "left" {
		return "左"
	}
	return "右"
}

// 5x5 投票采样（与 capture 命令一致）
func vote(img *image.RGBA, cx, cy int) (detect.BeadState, int, int, int) {
	var lightN, darkN, unkN int
	var lr, lg, lb, dr, dg, db int
	w, h := detect.BeadW, detect.BeadH
	for dy := -h / 2; dy <= h/2; dy++ {
		for dx := -w / 2; dx <= w/2; dx++ {
			x, y := cx+dx, cy+dy
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			if !detect.InBeadDiamond(cx, cy, w, h, x, y) {
				continue
			}
			c := img.RGBAAt(x, y)
			switch detect.Classify(int(c.R), int(c.G), int(c.B)) {
			case detect.StateLight:
				lightN++
				lr += int(c.R)
				lg += int(c.G)
				lb += int(c.B)
			case detect.StateDark:
				darkN++
				dr += int(c.R)
				dg += int(c.G)
				db += int(c.B)
			default:
				unkN++
			}
		}
	}
	if lightN > darkN && lightN > unkN {
		return detect.StateLight, lr / lightN, lg / lightN, lb / lightN
	}
	if darkN > lightN && darkN > unkN {
		return detect.StateDark, dr / darkN, dg / darkN, db / darkN
	}
	return detect.StateUnknown, 0, 0, 0
}
