// Owner: cpp-gated-frame
// Port of: internal/engine/layout.go
// Contract: include/nt/engine.h — see android/ARCHITECTURE.md.
//
// DefaultLayout 是默认豆位提供者：基于 detect 的 1920×1080 逻辑标定 +
// ComputeContentArea 等比映射。RGB 与模型引擎共用。
//
// 豆位标定目前硬编码在 detect.DefaultBeads()，后续可改为从配置文件加载
// （本结构预留了 Beads 字段，配置接入时直接替换数据源即可）。
#include "nt/engine.h"

#include <utility>

namespace nt::engine {

// NewDefaultLayout 用默认豆位标定 + 内容模式构造提供者。
DefaultLayout::DefaultLayout(detect::ContentMode mode) : mode_(mode), beads_(detect::DefaultBeads()) {}

// NewLayout 用自定义豆位标定构造提供者（配置接入后用）。
DefaultLayout::DefaultLayout(detect::ContentMode mode, std::vector<detect::Bead> beads)
    : mode_(mode), beads_(std::move(beads)) {
    if (beads_.empty()) {
        beads_ = detect::DefaultBeads();
    }
}

// Positions 返回当前截图尺寸下所有豆位的实际像素坐标。
std::vector<detect::BeadPosition> DefaultLayout::Positions(int w, int h) const {
    return detect::Layout(w, h, mode_, beads_);
}

}  // namespace nt::engine
