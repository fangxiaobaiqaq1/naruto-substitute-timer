// Package engine 定义检测引擎抽象：把"给一张截图 → 返回画面与各豆状态"
// 抽象为接口，判色/模型推理都是接口的实现，可热插拔。
//
// 设计目标（解耦 + 预留模型空间）：
//   - UI / 采集 / 状态机只面向 Engine 接口编程，不关心是 RGB 判色还是模型
//   - 当前默认实现是 RGBEngine（纯 RGB 区间判色，internal/engine/rgb）
//   - 后续新增模型引擎（如 ONNX）只需实现同一接口，配置里切换即可
//   - 豆位标定（Layout）抽离为可配置资源，RGB 与模型共用同一套豆位坐标
package engine

import (
	"image"
	"time"

	"narutotimer/internal/detect"
)

// Side 表示左侧/右侧。
type Side int

const (
	Left Side = iota
	Right
)

func (s Side) String() string {
	if s == Right {
		return "right"
	}
	return "left"
}

// BeadInfo 是一颗豆的判定结果（坐标已换算为截图像素，供 UI 标注）。
type BeadInfo struct {
	X, Y    int     // 豆心截图像素坐标
	Label   string  // 如 "L1"、"R3"
	Lit     bool    // true = 亮蓝（可用）
	Gold    bool    // true = 金色可用豆；Lit 同时为 true
	Unknown bool    // true = 这颗没看清，不能当暗豆
	Conf    float64 // 视觉证据得分 0~1，不是已校准的正确概率。
}

// Result 是一帧画面的完整分析结果。
type Result struct {
	TextStatus          string // Optional background OCR: waiting/reading/pending/ready/unavailable/off.
	TextError           string
	Fighting            bool       // 明确在决斗场
	Uncertain           bool       // 当前证据不足，不能用缓存的豆数确认事件。
	Beads               []BeadInfo // 检测到的豆子（左 4 + 右 4），可空
	Name                string
	LayoutProfile       string  // Actual coordinate profile used by the inner engine.
	Scene               string  // 门闩给出的场景 id，可空
	GateScore           float64 // 门闩最高分，可空
	LeftNinja           string
	RightNinja          string
	LeftNinjaCandidate  string // Display-only table match, never special-rule evidence.
	RightNinjaCandidate string
	LeftSlots           int
	RightSlots          int
	PlayerSide          string // 从 VS 底栏 / 对局顶部账号认出的我方 left/right，无新证据时为空
	PlayerName          string // 我方账号，来自 ui.playerNames
	OppName             string // 对面账号，能对上图鉴才有
}

// LayoutPreferrer receives only a template's explicit camp/duel binding.
type LayoutPreferrer interface {
	Prefer(kind string)
}

// Engine 是检测引擎接口：输入一张客户区截图，输出画面与豆状态。
//
// 实现约定：
//   - 支持并发调用；可保留布局偏好，但必须同步保护。
//   - img 为窗口客户区截图（与 detect.ComputeContentArea 同一坐标系）
//   - 返回的 BeadInfo 坐标必须是截图像素坐标（供 UI 标注/状态机使用）
type Engine interface {
	Analyze(img *image.RGBA) Result
}

// TimedEngine uses acquisition time so live capture and recorded replay share
// the same bounded scene continuity, independent of replay processing speed.
type TimedEngine interface {
	AnalyzeAt(img *image.RGBA, capturedAt time.Time) Result
}

// LayoutProvider 提供豆位标定（逻辑坐标 → 截图像素坐标）。
// RGB 与模型引擎共用同一套豆位，避免各自标定漂移。
type LayoutProvider interface {
	// Positions 返回当前截图尺寸下所有豆位的实际像素坐标。
	Positions(w, h int) []detect.BeadPosition
	// Mode 返回当前内容模式（auto/stretch/letterbox）。
	Mode() detect.ContentMode
}

// ProfiledLayout supplies independently calibrated camp/duel positions.
type ProfiledLayout interface {
	LayoutProvider
	PositionsFor(profile string, w, h int) []detect.BeadPosition
	PreferredProfile() string
}
