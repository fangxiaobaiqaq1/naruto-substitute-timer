package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strconv"
	"strings"

	"narutotimer/internal/config"
	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/identity"
	"narutotimer/internal/match"
	"narutotimer/internal/scene"
)

func cmdScene(args []string) error {
	fs := flag.NewFlagSet("scene", flag.ContinueOnError)
	cfgPath := fs.String("config", config.DefaultPath, "config.json 路径")
	manifest := fs.String("manifest", "", "覆盖 scene.manifest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("用法: timer scene [-config config.json] [-manifest path] <image.png>")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		if !os.IsNotExist(err) && !strings.Contains(err.Error(), "open config") {
			return err
		}
		cfg = config.Default()
	}
	if *manifest != "" {
		cfg.Scene.Enabled = true
		cfg.Scene.Manifest = *manifest
	}
	img, err := loadRGBA(fs.Arg(0))
	if err != nil {
		return err
	}
	gate, src, err := scene.NewGate(cfg)
	if err != nil {
		return err
	}
	fmt.Printf("门闩: %s\n", src)
	if cat, ok := gate.(*scene.Catalog); ok {
		for _, hit := range cat.ScoreAll(img) {
			fmt.Printf("  %-24s scene=%-8s score=%.3f\n", hit.ID, hit.Scene, hit.Value)
		}
	}
	d := gate.Decide(img)
	fmt.Printf("结论: %s  scene=%s  conf=%.3f\n", kindName(d.Kind), d.SceneID, d.Confidence)
	eng, err := factory.New(factory.FromApp(cfg))
	if err != nil {
		return err
	}
	res := eng.Analyze(img)
	fmt.Printf("引擎: fighting=%v uncertain=%v beads=%d scene=%s score=%.3f\n",
		res.Fighting, res.Uncertain, len(res.Beads), res.Scene, res.GateScore)
	if guess := identity.Guesser(cfg); guess != nil {
		r := guess(img, d.SceneID)
		if r.Side != "" || r.Mine != "" || r.Opp != "" {
			fmt.Printf("认人: 边=%s  我=%s  对面=%s\n", emptyDash(r.Side), emptyDash(r.Mine), emptyDash(r.Opp))
		}
	}
	return nil
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func cmdCrop(args []string) error {
	fs := flag.NewFlagSet("crop", flag.ContinueOnError)
	imagePath := fs.String("image", "", "源 PNG")
	roiStr := fs.String("roi", "", "归一化内容区 ROI: x,y,w,h （0~1）")
	outPath := fs.String("out", "", "输出 PNG")
	modeStr := fs.String("mode", "auto", "内容模式: auto/stretch/letterbox")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *imagePath == "" || *roiStr == "" || *outPath == "" {
		return fmt.Errorf("用法: timer crop -image dump.png -roi 0.42,0.02,0.16,0.10 -out assets/templates/foo.png")
	}
	mode, err := detect.ParseMode(*modeStr)
	if err != nil {
		return err
	}
	roi, err := parseROI(*roiStr)
	if err != nil {
		return err
	}
	img, err := loadRGBA(*imagePath)
	if err != nil {
		return err
	}
	b := img.Bounds()
	ca := detect.ComputeContentArea(b.Dx(), b.Dy(), mode)
	x0, y0 := ca.Map(roi.X*detect.LogicWidth, roi.Y*detect.LogicHeight)
	x1, y1 := ca.Map((roi.X+roi.Width)*detect.LogicWidth, (roi.Y+roi.Height)*detect.LogicHeight)
	crop := match.CropRGBA(img, image.Rect(x0, y0, x1, y1))
	if crop.Bounds().Empty() {
		return fmt.Errorf("裁切结果为空: roi=%v content=%+v", roi, ca)
	}
	// 存成 1920×1080 基准尺寸，匹配时再缩回当前内容区。
	refW := int(roi.Width*detect.LogicWidth + 0.5)
	refH := int(roi.Height*detect.LogicHeight + 0.5)
	if refW < 8 {
		refW = 8
	}
	if refH < 8 {
		refH = 8
	}
	outImg := match.ScaleGray(match.ToGray(crop), refW, refH)
	if err := os.MkdirAll(parentDir(*outPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(*outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := png.Encode(f, outImg); err != nil {
		return err
	}
	fmt.Printf("已裁 %s  %dx%d（基准） 源裁切 %dx%d  内容区 %dx%d 偏移 %d,%d\n",
		*outPath, refW, refH, crop.Bounds().Dx(), crop.Bounds().Dy(), ca.W, ca.H, ca.X, ca.Y)
	return nil
}

func loadRGBA(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba, nil
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			out.SetRGBA(x-b.Min.X, y-b.Min.Y, color.RGBA{
				R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bl >> 8), A: uint8(a >> 8),
			})
		}
	}
	return out, nil
}

func parseROI(s string) (config.NormalizedRect, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return config.NormalizedRect{}, fmt.Errorf("roi 应为 x,y,w,h")
	}
	var nums [4]float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return config.NormalizedRect{}, fmt.Errorf("roi[%d]: %w", i, err)
		}
		nums[i] = v
	}
	r := config.NormalizedRect{X: nums[0], Y: nums[1], Width: nums[2], Height: nums[3]}
	if r.Width <= 0 || r.Height <= 0 {
		return r, fmt.Errorf("roi 宽高必须为正")
	}
	return r, nil
}

func parentDir(path string) string {
	i := strings.LastIndexAny(path, `/\`)
	if i <= 0 {
		return "."
	}
	return path[:i]
}

func kindName(k engine.GateKind) string {
	switch k {
	case engine.GateFight:
		return "fight"
	case engine.GateNotFight:
		return "not-fight"
	case engine.GateBlank:
		return "blank"
	default:
		return "uncertain"
	}
}
