// Package detect 负责把游戏逻辑坐标映射到窗口实际坐标，并定义豆子位置。
package detect

import "fmt"

// 游戏画面逻辑分辨率（火影忍者手游决斗场 UI 以 1920×1080 为基准布局）。
const (
	LogicWidth  = 1920
	LogicHeight = 1080
)

// ContentMode 描述游戏画面在窗口客户区内的呈现方式。
type ContentMode int

const (
	// ModeAuto 自动判断：客户区宽高比接近 16:9 视为铺满（拉伸），否则按等比黑边。
	ModeAuto ContentMode = iota
	// ModeStretch 强制铺满客户区（无黑边，画面拉伸变形）。
	ModeStretch
	// ModeLetterbox 强制等比缩放（上下或左右黑边）。
	ModeLetterbox
)

// ParseMode 解析字符串为 ContentMode。
func ParseMode(s string) (ContentMode, error) {
	switch s {
	case "", "auto":
		return ModeAuto, nil
	case "stretch", "拉伸":
		return ModeStretch, nil
	case "letterbox", "等比", "黑边":
		return ModeLetterbox, nil
	}
	return ModeAuto, fmt.Errorf("未知内容模式 %q（可用: auto/stretch/letterbox）", s)
}

// ContentArea 是游戏画面在客户区内的实际区域（客户区坐标）。
type ContentArea struct {
	X, Y, W, H int
}

// ComputeContentArea 根据客户区尺寸与模式计算游戏画面实际区域。
func ComputeContentArea(clientW, clientH int, mode ContentMode) ContentArea {
	ca := ContentArea{X: 0, Y: 0, W: clientW, H: clientH}
	if clientW <= 0 || clientH <= 0 {
		return ca
	}

	letterbox := false
	switch mode {
	case ModeStretch:
		letterbox = false
	case ModeLetterbox:
		letterbox = true
	default: // ModeAuto
		// 客户区宽高比与 16:9 偏差小于 1.5% 视为铺满，否则视为有黑边。
		ratio := float64(clientW) / float64(clientH)
		if ratio < 16.0/9.0*0.985 || ratio > 16.0/9.0*1.015 {
			letterbox = true
		}
	}
	if !letterbox {
		return ca
	}

	// 等比缩放：以宽或高为准取较小缩放比。
	scaleW := float64(clientW) / LogicWidth
	scaleH := float64(clientH) / LogicHeight
	scale := scaleW
	if scaleH < scaleW {
		scale = scaleH
	}
	w := int(float64(LogicWidth) * scale)
	h := int(float64(LogicHeight) * scale)
	ca.W, ca.H = w, h
	ca.X = (clientW - w) / 2
	ca.Y = (clientH - h) / 2
	return ca
}

// Map 把 1920×1080 逻辑坐标映射到内容区内的实际坐标。
func (ca ContentArea) Map(lx, ly float64) (int, int) {
	x := ca.X + int(float64(ca.W)*lx/LogicWidth)
	y := ca.Y + int(float64(ca.H)*ly/LogicHeight)
	return x, y
}

// Bead 是一个替身豆。
type Bead struct {
	Side string  // "left" / "right"
	Idx  int     // 0..n-1，创立柱间等最多 6
	LX   float64 // 逻辑 X（0~1920）
	LY   float64 // 逻辑 Y（0~1080）
}

// BeadState 是替身豆的视觉状态。
type BeadState int

const (
	// StateUnknown 未知/未检测。
	StateUnknown BeadState = iota
	// StateDark 暗色 = 替身已释放，冷却中（倒计时起点）。
	StateDark
	// StateLight 亮色 = 替身可用。
	StateLight
	// StateGone 消失 = 无豆（未入场/已阵亡）。
	StateGone
)

func (s BeadState) String() string {
	switch s {
	case StateDark:
		return "暗(冷却中)"
	case StateLight:
		return "亮(可用)"
	case StateGone:
		return "消失"
	default:
		return "未知"
	}
}

// defaultBeadLayout 是训练场四槽豆子的逻辑坐标（1920×1080 基准）。
//
// UI 层级（从上到下）：角色名字 → 血条 → 替身豆。
// 血条是连续的橙/红色横条（y≈88~94 客户区，勿与豆子混淆）；
// 豆子位于血条正下方一行（客户区 y≈100~118，内容区 y≈80~98）。
//
// 充能规则：按 1→n 递增，前面亮了后面才可能亮。
// 六槽必须通过显式配置标定，不把血条/木纹当作默认第五、第六槽。
// 右侧镜像：豆1 在最右，数组按右→左。间隔 30px。
//
// 标定：4 豆来自实机（188/218/248/278 @ y=155）。
var defaultBeadLayout = []Bead{
	{Side: "left", Idx: 0, LX: 188, LY: 155},
	{Side: "left", Idx: 1, LX: 218, LY: 155},
	{Side: "left", Idx: 2, LX: 248, LY: 155},
	{Side: "left", Idx: 3, LX: 278, LY: 155},

	{Side: "right", Idx: 0, LX: 1674, LY: 155},
	{Side: "right", Idx: 1, LX: 1644, LY: 155},
	{Side: "right", Idx: 2, LX: 1614, LY: 155},
	{Side: "right", Idx: 3, LX: 1584, LY: 155},
}

// duelBeadLayout 真决斗场 HUD 比训练营整排偏右约 21 逻辑像素。
// 985×594 实机：训练营左豆 x≈96/111/127/142，决斗场 x≈107/122/137/152。
var duelBeadLayout = []Bead{
	{Side: "left", Idx: 0, LX: 209, LY: 174},
	{Side: "left", Idx: 1, LX: 239, LY: 174},
	{Side: "left", Idx: 2, LX: 269, LY: 174},
	{Side: "left", Idx: 3, LX: 299, LY: 174},

	{Side: "right", Idx: 0, LX: 1688, LY: 174},
	{Side: "right", Idx: 1, LX: 1658, LY: 174},
	{Side: "right", Idx: 2, LX: 1628, LY: 174},
	{Side: "right", Idx: 3, LX: 1604, LY: 174},
}

// DefaultBeads 返回训练营豆位（副本）。
func DefaultBeads() []Bead {
	out := make([]Bead, len(defaultBeadLayout))
	copy(out, defaultBeadLayout)
	return out
}

// DuelBeads 返回真决斗场豆位（副本）。
func DuelBeads() []Bead {
	out := make([]Bead, len(duelBeadLayout))
	copy(out, duelBeadLayout)
	return out
}

// BeadPosition 是豆子在屏幕上的实际位置。
type BeadPosition struct {
	Bead
	X, Y int
}

// Layout 计算所有豆子在实际客户区内的屏幕坐标。
func Layout(clientW, clientH int, mode ContentMode, beads []Bead) []BeadPosition {
	ca := ComputeContentArea(clientW, clientH, mode)
	pos := make([]BeadPosition, 0, len(beads))
	for _, b := range beads {
		x, y := ca.Map(b.LX, b.LY)
		pos = append(pos, BeadPosition{Bead: b, X: x, Y: y})
	}
	return pos
}

// 颜色三态阈值，取自参考项目标定（1920×1080 基准下实测）。
var (
	// DarkRange 暗青 = 冷却中。描边/阴影会稍亮一点。
	DarkRange = ColorRange{Min: [3]int{8, 3, 1}, Max: [3]int{70, 90, 130}}
	// LightBlueRange 亮蓝/暗青高光都算可用。决斗场菱形中间有暗带，整体比训练营暗。
	LightBlueRange = ColorRange{Min: [3]int{0, 90, 115}, Max: [3]int{255, 255, 255}}
	// GoldRange 金豆（可用）。血条/爆炸是红橙，绿没这么高。
	GoldRange = ColorRange{Min: [3]int{190, 150, 0}, Max: [3]int{255, 255, 90}}
	// Tolerance 每通道允许的额外容差
	Tolerance = 4
)

// ColorRange 是一个 RGB 区间。
type ColorRange struct {
	Min, Max [3]int
}

// Contains 判断 RGB 是否落在区间内（含容差）。
func (c ColorRange) Contains(r, g, b int) bool {
	return r >= c.Min[0]-Tolerance && r <= c.Max[0]+Tolerance &&
		g >= c.Min[1]-Tolerance && g <= c.Max[1]+Tolerance &&
		b >= c.Min[2]-Tolerance && b <= c.Max[2]+Tolerance
}

// Classify 把 RGB 归类为豆子状态。
// 金豆和亮蓝都是可用；纯白是空帧，不能当亮豆。
func Classify(r, g, b int) BeadState {
	if isNearWhite(r, g, b) {
		return StateUnknown
	}
	if IsGold(r, g, b) {
		return StateLight
	}
	switch {
	case LightBlueRange.Contains(r, g, b):
		return StateLight
	case DarkRange.Contains(r, g, b):
		return StateDark
	default:
		return StateUnknown
	}
}

// IsGold 判断是否金豆（可用），不是血条红橙。
func IsGold(r, g, b int) bool {
	if !GoldRange.Contains(r, g, b) {
		return false
	}
	// 金豆偏黄：G 接近 R。爆炸/血条偏红：R 远大于 G。
	return g-b >= 90 && r-g <= 70 && b <= 90
}

func isNearWhite(r, g, b int) bool {
	return r >= 245 && g >= 245 && b >= 245
}

// ApplyIncrementRule 按充能递增规则处理一侧豆子。
// 合法前缀原样返回；非法整侧标未知，绝不改写成“亮了 N 颗”。
func ApplyIncrementRule(states []BeadState) []BeadState {
	out := append([]BeadState(nil), states...)
	if IsLegalPrefix(out) {
		return out
	}
	for i := range out {
		out[i] = StateUnknown
	}
	return out
}

// IsLegalPrefix 亮豆必须是从豆1 起的连续前缀。末尾空槽（4 豆角色的 5/6）忽略。
func IsLegalPrefix(states []BeadState) bool {
	n := len(states)
	for n > 0 && (states[n-1] == StateUnknown || states[n-1] == StateGone) {
		n--
	}
	if n == 0 {
		return false
	}
	seenDark := false
	for _, s := range states[:n] {
		if s == StateUnknown || s == StateGone {
			return false
		}
		if s == StateLight {
			if seenDark {
				return false
			}
			continue
		}
		seenDark = true
	}
	return true
}

// IsPossiblePrefix checks only contradictions in CURRENT known observations.
// An obscured slot cannot erase its readable neighbors or supply their state.
// This is not a complete count: callers still require every slot before voting
// a bean event. A known dark followed by a known light remains impossible.
func IsPossiblePrefix(states []BeadState) bool {
	seenDark := false
	for _, s := range states {
		switch s {
		case StateDark:
			seenDark = true
		case StateLight:
			if seenDark {
				return false
			}
		}
	}
	return true
}

// CountLight 统计一侧豆子中亮（可用）的数量。
func CountLight(states []BeadState) int {
	n := 0
	for _, s := range states {
		if s == StateLight {
			n++
		}
	}
	return n
}

// 豆子原生形状（像素级实测，客户区 1092×654 决斗场画面）：
//   - 替身豆是竖向菱形：顶部尖点 → 中部最宽 → 底部尖点
//   - 亮蓝主体实测 11px 宽 × 16px 高；暗色描边外还有约 1px 发光
//   - 用户参考图（豆子特写）：菱形中部有一条横向暗青装饰带，勿当作两颗粒子
//
// 检测区域取豆心 7×10 菱形（小于主体 11×16），只判状态，不包裹整颗豆。
const (
	BeadW = 7  // 检测菱形宽（客户区像素，1092 基准）
	BeadH = 10 // 检测菱形高
)

// InBeadDiamond 判断点 (x,y) 是否落在以 (cx,cy) 为中心、宽 w 高 h 的菱形内。
// 菱形方程：|dx|/(w/2) + |dy|/(h/2) <= 1，全部整数运算避免浮点误差。
func InBeadDiamond(cx, cy, w, h, x, y int) bool {
	a, b := w/2, h/2
	dx, dy := x-cx, y-cy
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx*b+dy*a <= a*b
}
