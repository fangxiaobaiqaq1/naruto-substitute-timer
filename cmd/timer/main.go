// narutotimer — 火影忍者手游替身计时器（纯视觉，不读内存）。
// 本期功能：
//   - win:   查找 MuMu 模拟器窗口
//   - beads: 根据窗口客户区计算替身豆的实际屏幕坐标（支持任意窗口大小/分辨率）
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

	"narutotimer/internal/detect"
	"narutotimer/internal/overlay"
	"narutotimer/internal/win"
)

// 子命令 scene / crop 在 scene.go。

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "win":
		err = cmdWin(os.Args[2:])
	case "beads":
		err = cmdBeads(os.Args[2:])
	case "capture":
		err = cmdCapture(os.Args[2:])
	case "list":
		err = cmdList()
	case "overlay":
		err = cmdOverlay(os.Args[2:])
	case "scene":
		err = cmdScene(os.Args[2:])
	case "crop":
		err = cmdCrop(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`用法:
  narutotimer win          查找 MuMu 模拟器窗口并显示其客户区
  narutotimer beads        根据窗口客户区计算替身豆的实际坐标
  narutotimer capture      截取窗口客户区保存 PNG，并在豆子位置采样 RGB
  narutotimer list         列出所有顶层窗口（调试用）
	  narutotimer overlay      在 MuMu 窗口上叠加豆位菱形框（校准用，回车关闭）
	  narutotimer scene        对一张 PNG 跑场景门闩，打印各模板分数
	  narutotimer crop         按归一化 ROI 从截图裁一块，供入库`)
}

// cmdOverlay 在 MuMu 窗口上叠加豆位菱形框（校准用）。
// 覆盖层透明、鼠标穿透、跟随窗口移动；按回车关闭。
func cmdOverlay(args []string) error {
	fs := flag.NewFlagSet("overlay", flag.ContinueOnError)
	colorHex := fs.String("color", "FF00FF", "菱形框颜色 RRGGBB（默认亮紫，游戏中罕见便于核对）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	w, err := findFirstMuMu()
	if err != nil {
		return err
	}
	var col uint32
	if _, err := fmt.Sscanf(*colorHex, "%06X", &col); err != nil {
		return fmt.Errorf("颜色格式错误（应 RRGGBB）: %v", err)
	}
	// RGB → BGR（CreatePen 用 COLORREF 0x00BBGGRR）
	bgr := (col&0xFF)<<16 | (col & 0xFF00) | (col>>16)&0xFF
	cfg := overlay.DefaultConfig()
	cfg.BeadColor = bgr

	ov, err := overlay.Start(w.HWND, cfg)
	if err != nil {
		return fmt.Errorf("启动覆盖层失败: %v", err)
	}
	fmt.Printf("覆盖层已启动：%s (HWND=0x%X)\n", w.Title, w.HWND)
	fmt.Println("菱形框已叠加在 MuMu 窗口上（跟随窗口、鼠标穿透）。按回车关闭…")
	buf := make([]byte, 1)
	os.Stdin.Read(buf)
	ov.Stop()
	fmt.Println("覆盖层已关闭")
	return nil
}

// findFirstMuMu 查找 MuMu 主窗口（设备进程优先），若最小化则恢复。
func findFirstMuMu() (win.Window, error) {
	wins := win.FindMuMu()
	if len(wins) == 0 {
		return win.Window{}, fmt.Errorf("未找到 MuMu 模拟器窗口，请先启动模拟器（可用 narutotimer list 查看所有窗口）")
	}
	// 设备进程优先：优先选设备/游戏进程的窗口
	w := wins[0]
	for _, cand := range wins {
		if strings.Contains(strings.ToLower(cand.ProcessName), "device") {
			w = cand
			break
		}
	}
	// 若最小化则恢复，避免截到 152x24 的缩略窗口
	if err := win.RestoreWindow(w.HWND); err != nil {
		fmt.Fprintf(os.Stderr, "警告: 恢复窗口失败: %v\n", err)
	}
	return w, nil
}

func cmdWin(args []string) error {
	fs := flag.NewFlagSet("win", flag.ContinueOnError)
	all := fs.Bool("all", false, "显示所有匹配窗口而不只第一个")
	if err := fs.Parse(args); err != nil {
		return err
	}

	wins := win.FindMuMu()
	if len(wins) == 0 {
		fmt.Println("未找到 MuMu 模拟器窗口。以下为所有可见窗口：")
		return cmdList()
	}
	for i, w := range wins {
		cr, err := win.ClientScreenRect(w.HWND)
		if err != nil {
			fmt.Printf("[%d] %s (PID=%d, HWND=0x%X): 获取客户区失败: %v\n", i, w.Title, w.PID, w.HWND, err)
			continue
		}
		fmt.Printf("[%d] %s\n", i, w.Title)
		fmt.Printf("    进程: %s  PID=%d  HWND=0x%X  类名=%s\n", w.ProcessName, w.PID, w.HWND, w.ClassName)
		fmt.Printf("    客户区: 屏幕(%d,%d) 大小 %dx%d\n", cr.Left, cr.Top, cr.Width(), cr.Height())
		if !*all {
			break
		}
	}
	return nil
}

func cmdBeads(args []string) error {
	fs := flag.NewFlagSet("beads", flag.ContinueOnError)
	modeStr := fs.String("mode", "auto", "内容模式: auto/stretch/letterbox")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mode, err := detect.ParseMode(*modeStr)
	if err != nil {
		return err
	}

	w, err := findFirstMuMu()
	if err != nil {
		return err
	}
	cr, err := win.ClientScreenRect(w.HWND)
	if err != nil {
		return fmt.Errorf("获取窗口客户区失败: %v", err)
	}

	clientW, clientH := int(cr.Width()), int(cr.Height())
	ca := detect.ComputeContentArea(clientW, clientH, mode)
	positions := detect.Layout(clientW, clientH, mode, detect.DefaultBeads())

	fmt.Printf("窗口: %s (PID=%d)\n", w.Title, w.PID)
	fmt.Printf("客户区: 屏幕(%d,%d) 大小 %dx%d\n", cr.Left, cr.Top, cr.Width(), cr.Height())
	if ca.W != clientW || ca.H != clientH {
		fmt.Printf("内容区(等比): 偏移(%d,%d) 大小 %dx%d\n", ca.X, ca.Y, ca.W, ca.H)
	} else {
		fmt.Println("内容区: 铺满客户区（无黑边）")
	}

	lastSide := ""
	for _, p := range positions {
		if p.Side != lastSide {
			fmt.Printf("\n%s 侧:\n", mapSide(p.Side))
			lastSide = p.Side
		}
		fmt.Printf("  豆%d  屏幕(%d, %d)  [逻辑 %.0f, %.0f]\n", p.Idx+1, p.X, p.Y, p.LX, p.LY)
	}

	// 输出 JSON 友好的紧凑形式，方便后续配置使用
	fmt.Println("\n坐标JSON:")
	fmt.Print("{\"left\":[")
	for i, p := range positions {
		if p.Side != "left" {
			continue
		}
		if i > 0 {
			fmt.Print(",")
		}
		fmt.Printf("[%d,%d]", p.X, p.Y)
	}
	fmt.Print("],\"right\":[")
	first := true
	for _, p := range positions {
		if p.Side != "right" {
			continue
		}
		if !first {
			fmt.Print(",")
		}
		first = false
		fmt.Printf("[%d,%d]", p.X, p.Y)
	}
	fmt.Println("]}")
	return nil
}

func cmdCapture(args []string) error {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	modeStr := fs.String("mode", "auto", "内容模式: auto/stretch/letterbox")
	out := fs.String("out", "capture.png", "PNG 输出路径")
	annotated := fs.String("annotated", "annotated.png", "豆子框选标注图输出路径（空=不生成）")
	beadProbe := fs.Bool("beads", true, "是否在豆子位置采样 RGB")
	hwndStr := fs.String("hwnd", "", "指定窗口句柄(十六进制)，默认自动查找 MuMu 主窗口")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mode, err := detect.ParseMode(*modeStr)
	if err != nil {
		return err
	}

	var w win.Window
	if *hwndStr != "" {
		s := strings.TrimPrefix(*hwndStr, "0x")
		s = strings.TrimPrefix(s, "0X")
		h, err := strconv.ParseUint(s, 16, 64)
		if err != nil {
			return fmt.Errorf("无效窗口句柄 %q: %v", *hwndStr, err)
		}
		w = win.Window{HWND: uintptr(h)}
	} else {
		w, err = findFirstMuMu()
		if err != nil {
			return err
		}
	}
	cr, err := win.ClientScreenRect(w.HWND)
	if err != nil {
		return fmt.Errorf("获取窗口客户区失败: %v", err)
	}
	clientW, clientH := int(cr.Width()), int(cr.Height())

	img, err := win.CaptureClient(w.HWND)
	if err != nil {
		return fmt.Errorf("截屏失败: %v", err)
	}

	// 保存 PNG
	if err := savePNG(img, *out); err != nil {
		return err
	}
	fmt.Printf("窗口: %s (PID=%d)\n", w.Title, w.PID)
	fmt.Printf("客户区: 屏幕(%d,%d) 大小 %dx%d\n", cr.Left, cr.Top, cr.Width(), cr.Height())
	fmt.Printf("截图已保存: %s (%dx%d)\n", *out, img.Bounds().Dx(), img.Bounds().Dy())

	if !*beadProbe {
		return nil
	}

	// 先判断画面状态：只有决斗场对局中豆子检测才有意义
	scrState := detect.ClassifyScreen(img, mode)
	fmt.Printf("画面状态: %s\n", scrState)

	// 在豆子位置采样（豆位坐标 = 客户区相对坐标，截图原点即客户区左上角）
	positions := detect.Layout(clientW, clientH, mode, detect.DefaultBeads())
	lastSide := ""
	sideStates := map[string][]detect.BeadState{}
	for _, p := range positions {
		if p.Side != lastSide {
			fmt.Printf("\n%s 侧:\n", mapSide(p.Side))
			lastSide = p.Side
		}
		state, r, g, b := sampleBeadVote(img, p.X, p.Y)
		fmt.Printf("  豆%d (%3d,%3d)  RGB(%3d,%3d,%3d)  → %s\n",
			p.Idx+1, p.X, p.Y, r, g, b, state)
		sideStates[p.Side] = append(sideStates[p.Side], state)
	}

	// 应用充能递增规则：亮豆必须是连续前缀，违反则按亮豆数修正
	if scrState == detect.ScreenFighting {
		for side, states := range sideStates {
			fixed := detect.ApplyIncrementRule(states)
			changed := false
			for i := range states {
				if states[i] != fixed[i] {
					changed = true
				}
			}
			if changed {
				fmt.Printf("%s 侧违反递增规则 %v，修正为 %v（亮 %d 颗）\n",
					mapSide(side), states, fixed, detect.CountLight(fixed))
			} else {
				fmt.Printf("%s 侧符合递增规则：亮 %d 颗\n", mapSide(side), detect.CountLight(fixed))
			}
		}
	}

	// 生成框选标注图：每个豆子画红框 + 中心十字，供人工审核
	if *annotated != "" {
		drawBeadBoxes(img, positions)
		if err := savePNG(img, *annotated); err != nil {
			return fmt.Errorf("保存标注图失败: %v", err)
		}
		fmt.Printf("\n标注图已保存: %s（红框=豆子位置，请人工核对）\n", *annotated)
	}
	return nil
}

// drawBeadBoxes 在截图上以豆子位置为中心画细线红色菱形边框（1px）。
// 菱形贴合豆子原生轮廓（竖向菱形，宽:高 ≈ 11:16 + 描边容错）。
// 只描边框、不加十字和填充，避免遮挡豆子细节。
// 框尺寸按逻辑坐标 23x32 映射到客户区（豆子 13x18 客户区像素 @1092 基准）。
func drawBeadBoxes(img *image.RGBA, positions []detect.BeadPosition) {
	// 以逻辑 1920 为基准计算缩放
	scale := float64(img.Bounds().Dx()) / 1920.0
	bw := int(23 * scale)
	bh := int(32 * scale)
	if bw < 8 {
		bw = 8
	}
	if bh < 10 {
		bh = 10
	}
	red := color.RGBA{R: 255, G: 40, B: 40, A: 255}
	for _, p := range positions {
		// 菱形四顶点：上 (x, y-bh/2)、右 (x+bw/2, y)、下 (x, y+bh/2)、左 (x-bw/2, y)
		topX, topY := p.X, p.Y-bh/2
		rightX, rightY := p.X+bw/2, p.Y
		botX, botY := p.X, p.Y+bh/2
		leftX, leftY := p.X-bw/2, p.Y
		drawLine(img, topX, topY, rightX, rightY, red)
		drawLine(img, rightX, rightY, botX, botY, red)
		drawLine(img, botX, botY, leftX, leftY, red)
		drawLine(img, leftX, leftY, topX, topY, red)
	}
}

// drawLine 用 Bresenham 算法画任意斜率的直线。
func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	b := img.Bounds()
	dx, dy := x1-x0, y1-y0
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	sx, sy := 1, 1
	if x1 < x0 {
		sx = -1
	}
	if y1 < y0 {
		sy = -1
	}
	err := dx - dy
	for {
		if x0 >= 0 && x0 < b.Dx() && y0 >= 0 && y0 < b.Dy() {
			img.SetRGBA(x0, y0, c)
		}
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

func drawHLine(img *image.RGBA, x0, y, x1 int, c color.RGBA) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	b := img.Bounds()
	for x := x0; x <= x1; x++ {
		if x >= 0 && x < b.Dx() && y >= 0 && y < b.Dy() {
			img.SetRGBA(x, y, c)
		}
	}
}

func drawVLine(img *image.RGBA, x, y0, y1 int, c color.RGBA) {
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	b := img.Bounds()
	for y := y0; y <= y1; y++ {
		if x >= 0 && x < b.Dx() && y >= 0 && y < b.Dy() {
			img.SetRGBA(x, y, c)
		}
	}
}

// sampleBead 采样豆子中心点及其邻域的平均 RGB（3x3），抗单点噪点。
// sampleBeadVote 在豆子菱形区域内逐像素分类并投票，返回多数状态与代表色。
// 豆子亮核小（约 4~6px）且带暗色边缘环，平均法会把亮豆拉成未知色，必须投票；
// 投票区域用豆子原生菱形（BeadW×BeadH），贴合轮廓，避免方形邻域采到环境特效。
func sampleBeadVote(img *image.RGBA, x, y int) (detect.BeadState, int, int, int) {
	w, h := detect.BeadW, detect.BeadH
	var darkN, lightN, otherN int
	var dr, dg, db, lr, lg, lb int // 各类颜色累计
	for dy := -h / 2; dy <= h/2; dy++ {
		for dx := -w / 2; dx <= w/2; dx++ {
			if !detect.InBeadDiamond(x, y, w, h, x+dx, y+dy) {
				continue
			}
			px, py := x+dx, y+dy
			if px < 0 || py < 0 || px >= img.Bounds().Dx() || py >= img.Bounds().Dy() {
				continue
			}
			c := img.RGBAAt(px, py)
			switch detect.Classify(int(c.R), int(c.G), int(c.B)) {
			case detect.StateDark:
				darkN++
				dr += int(c.R)
				dg += int(c.G)
				db += int(c.B)
			case detect.StateLight:
				lightN++
				lr += int(c.R)
				lg += int(c.G)
				lb += int(c.B)
			default:
				otherN++
			}
		}
	}
	switch {
	case lightN > darkN:
		return detect.StateLight, lr / lightN, lg / lightN, lb / lightN
	case darkN > 0:
		return detect.StateDark, dr / darkN, dg / darkN, db / darkN
	default:
		return detect.StateUnknown, 0, 0, 0
	}
}

func savePNG(img *image.RGBA, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func cmdList() error {
	// 默认只显示可见且有标题/类名的窗口；-all 显示全部（含无意义窗口）。
	all := false
	if len(os.Args) > 2 && os.Args[2] == "-all" {
		all = true
	}
	wins := win.ListWindows()
	shown := 0
	fmt.Println("顶层窗口:")
	for i, w := range wins {
		if !all {
			if !w.Visible || (w.Title == "" && w.ClassName == "") {
				continue
			}
		}
		mark := " "
		if w.Visible {
			mark = "V"
		}
		cr, err := win.ClientScreenRect(w.HWND)
		size := ""
		if err == nil && cr.Width() > 0 && cr.Height() > 0 {
			size = strconv.Itoa(int(cr.Width())) + "x" + strconv.Itoa(int(cr.Height()))
		}
		fmt.Printf("[%s] %-4d PID=%-7d 0x%-9X %-12s %-10s %s\n",
			mark, i, w.PID, w.HWND, w.ProcessName, size, truncate(w.Title, 40))
		shown++
	}
	fmt.Printf("共显示 %d 个窗口\n", shown)
	return nil
}

func mapSide(s string) string {
	if s == "right" {
		return "右"
	}
	return "左"
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
