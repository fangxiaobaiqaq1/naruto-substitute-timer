package detect

import "image"

// ScreenState 是游戏窗口当前画面状态。
type ScreenState int

const (
	// ScreenUnknown 无法判断。
	ScreenUnknown ScreenState = iota
	// ScreenFighting 决斗场对局中（豆子行存在豆子特征）。
	ScreenFighting
	// ScreenBlank 白屏/黑屏（加载、切后台、弹窗全屏遮罩）。
	ScreenBlank
	// ScreenOther 其他界面（主菜单、结算、选人等）。
	ScreenOther
)

// String 返回状态的中文名。
func (s ScreenState) String() string {
	switch s {
	case ScreenFighting:
		return "决斗场对局中"
	case ScreenBlank:
		return "白屏/黑屏（非对局）"
	case ScreenOther:
		return "其他界面（非对局）"
	default:
		return "未知"
	}
}

// 亮度阈值。
const (
	blankDarkTh  = 25.0  // 平均亮度低于此视为黑屏
	blankBrightT = 235.0 // 平均亮度高于此视为白屏
)

// ClassifyScreen 判断截图属于哪种画面状态。
// 判据：
//  1. 整体平均亮度过暗/过亮 → 白屏黑屏；
//  2. 豆子行区域（内容区 y≈80~98）内三态颜色（暗青/亮蓝/赤金）像素占比
//     达到豆子特征密度 → 决斗场对局；
//  3. 其余 → 其他界面。
//
// img 为窗口客户区截图（与 ComputeContentArea 同一坐标系）。
func ClassifyScreen(img *image.RGBA, mode ContentMode) ScreenState {
	bounds := img.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return ScreenUnknown
	}

	// 整体亮度采样（隔行隔列，快）
	var sum, n float64
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 8 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 8 {
			c := img.RGBAAt(x, y)
			sum += 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
			n++
		}
	}
	avg := sum / n
	if avg < blankDarkTh || avg > blankBrightT {
		return ScreenBlank
	}

	// Use the same pixel-backed content transform as the bead/name engines.
	// ComputeContentArea assumes the whole capture is game content and is
	// therefore wrong for black bars, window scaling and non-zero origins.
	ca, supported := ResolveContentArea(img, mode, LogicWidth, LogicHeight, 0.015)
	if !supported {
		return ScreenUnknown
	}
	// 豆子行：覆盖名字/血条下方的一整段窄带。真实帧的标题、系统
	// 浮层或缩放可能让豆子上下漂几像素，不能只扫旧的 80~98 窄带。
	// 这是 HUD ROI，不是中央 16:9 场景 ROI；按 fixed-height + edge
	// anchor 映射，超宽屏不会把扫描带横向拉伸到屏幕中间。
	left0 := ca.MapHUDPixel(60, 60, "left", LogicWidth, LogicHeight)
	left1 := ca.MapHUDPixel(360, 170, "left", LogicWidth, LogicHeight)
	right0 := ca.MapHUDPixel(1560, 60, "right", LogicWidth, LogicHeight)
	right1 := ca.MapHUDPixel(1860, 170, "right", LogicWidth, LogicHeight)
	y0 := max(min(left0.Y, left1.Y), bounds.Min.Y)
	y1 := min(max(left0.Y, left1.Y), bounds.Max.Y)
	xL0 := max(min(left0.X, left1.X), bounds.Min.X)
	xL1 := min(max(left0.X, left1.X), bounds.Max.X)
	xR0 := max(min(right0.X, right1.X), bounds.Min.X)
	xR1 := min(max(right0.X, right1.X), bounds.Max.X)

	var beadPix, leftPix, rightPix, total int
	for y := y0; y < y1; y++ {
		for side, reg := range [2][2]int{{xL0, xL1}, {xR0, xR1}} {
			for x := reg[0]; x < reg[1]; x++ {
				c := img.RGBAAt(x, y)
				state := Classify(int(c.R), int(c.G), int(c.B))
				// Dark scenery is common; only count a dark pixel when it has
				// the cyan chroma of a real cooldown bead.
				colored := state == StateLight || (state == StateDark && int(c.B)-int(c.R) >= 15 && int(c.B)-int(c.G) >= 8)
				if colored {
					beadPix++
					if side == 0 {
						leftPix++
					} else {
						rightPix++
					}
				}
				total++
			}
		}
	}
	// This is deliberately a bilateral fallback: it is only used when the
	// template gate has no usable scene marker, so a colorful single-sided page
	// must not open the fight state.
	if total > 0 && leftPix*100 >= total*1/2 && rightPix*100 >= total*1/2 && beadPix*100 >= total*2 {
		return ScreenFighting
	}
	return ScreenOther
}
