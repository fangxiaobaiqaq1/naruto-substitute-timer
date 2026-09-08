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

	// 豆子行：略放宽，避免标题栏/缩放把豆挤出旧的 80~98 窄带。
	ca := ComputeContentArea(bounds.Dx(), bounds.Dy(), mode)
	y0 := ca.Y + ca.H*70/LogicHeight
	y1 := ca.Y + ca.H*130/LogicHeight
	xL0 := ca.X + ca.W*80/LogicWidth
	xL1 := ca.X + ca.W*340/LogicWidth
	xR0 := ca.X + ca.W*1560/LogicWidth
	xR1 := ca.X + ca.W*1800/LogicWidth

	var beadPix, total int
	for y := y0; y < y1; y++ {
		for _, reg := range [2][2]int{{xL0, xL1}, {xR0, xR1}} {
			for x := reg[0]; x < reg[1]; x++ {
				c := img.RGBAAt(x, y)
				if Classify(int(c.R), int(c.G), int(c.B)) != StateUnknown {
					beadPix++
				}
				total++
			}
		}
	}
	if total > 0 && float64(beadPix)/float64(total) > 0.08 {
		return ScreenFighting
	}
	return ScreenOther
}
