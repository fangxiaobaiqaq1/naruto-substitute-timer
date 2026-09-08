package engine

import "narutotimer/internal/detect"

// DefaultLayout 是默认豆位提供者：基于 detect 的 1920×1080 逻辑标定 +
// ComputeContentArea 等比映射。RGB 与模型引擎共用。
//
// 豆位标定目前硬编码在 detect.DefaultBeads()，后续可改为从配置文件加载
// （本结构预留了 Beads 字段，配置接入时直接替换数据源即可）。
type DefaultLayout struct {
	mode  detect.ContentMode
	beads []detect.Bead
}

// NewDefaultLayout 用默认豆位标定 + 内容模式构造提供者。
func NewDefaultLayout(mode detect.ContentMode) *DefaultLayout {
	return &DefaultLayout{mode: mode, beads: detect.DefaultBeads()}
}

// NewLayout 用自定义豆位标定构造提供者（配置接入后用）。
func NewLayout(mode detect.ContentMode, beads []detect.Bead) *DefaultLayout {
	if len(beads) == 0 {
		beads = detect.DefaultBeads()
	}
	return &DefaultLayout{mode: mode, beads: beads}
}

// Positions 返回当前截图尺寸下所有豆位的实际像素坐标。
func (l *DefaultLayout) Positions(w, h int) []detect.BeadPosition {
	return detect.Layout(w, h, l.mode, l.beads)
}

// Mode 返回当前内容模式。
func (l *DefaultLayout) Mode() detect.ContentMode { return l.mode }
