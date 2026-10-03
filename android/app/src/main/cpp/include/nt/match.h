// nt/match.h — internal/match（owner: cpp-match-detect；实现 src/match/match.cpp, src/match/prepared.cpp）
//
// Package match 提供与游戏无关的模板匹配器。
// 第一实现是灰度 NCC；以后可换哈希 / 其它 Matcher，场景门闩不用改。
//
// 逐位一致要求：PreparedNCC::at 的整数累加与浮点公式必须与 Go 完全相同
// （同样的运算顺序，exact integer sums），粗扫 coarseStep/refineStep 一致。
#pragma once

#include <cstdint>
#include <memory>
#include <string>
#include <vector>

#include "nt/assets.h"
#include "nt/image.h"

namespace nt::match {

// Score 是一次匹配的峰值结果。
struct Score {
    double Value = 0;      // NCC，截到 [0,1] 后再和阈值比
    Point Peak;            // 搜索框内峰值左上角（截图像素）
    std::string Template;  // 仅诊断用（Go: tb.String()）
};

class PreparedNCC;

// Query 描述一次匹配。ROI 已是截图像素；Template / Mask 为灰度。
struct Query {
    const nt::RGBA* Image = nullptr;
    const nt::Gray* Gray = nullptr;  // 可选，同一截图的灰度；尺寸须与 Image 一致，原点可不同
    nt::Rect ROI;
    const nt::Gray* Template = nullptr;
    const nt::Gray* Mask = nullptr;               // 可选，0 = 忽略
    const PreparedNCC* Prepared = nullptr;        // Optional immutable template+mask snapshot; authoritative when set.
};

// Matcher 把一块图和一张模板比出分数。返回 false 等价于 Go 的 err != nil。
class Matcher {
public:
    virtual ~Matcher() = default;
    virtual bool Match(const Query& q, Score& out) const = 0;
};

// NCC 是归一化互相关实现。Match 在 ROI 内滑动模板，返回最高 NCC。
class NCC final : public Matcher {
public:
    bool Match(const Query& q, Score& out) const override;
};

// grayPrefix holds per-row prefix sums of gray and gray² over one rectangle
// of a Gray image, in Pix offsets relative to Bounds().Min. Row r has w+1
// entries; entry k is the sum of the first k pixels. Built once per Match call
// (Go 用 sync.Pool 复用；C++ 用 thread_local 复用缓冲)。
struct grayPrefix {
    int x0 = 0, y0 = 0, w = 0;
    std::vector<uint64_t> sum, square;
};

// maxDotRun bounds one uint32 dot accumulation: 255*255*maxDotRun < 2^32.
constexpr int maxDotRun = 66051;

// PreparedNCC owns a snapshot of one template and its binary mask. It is
// immutable and safe to share between matchers.
class PreparedNCC {
public:
    struct nccRun {
        int x = 0, y = 0;
        int offset = 0;  // 在 data_ 中的起始下标
        int len = 0;
    };

    const Rect& rect() const { return rect_; }
    double count() const { return count_; }

    // at scores the template at (ox, oy) (offsets relative to img.Bounds().Min).
    // pre must cover the template footprint. Bit-identical to Go.
    double at(const nt::Gray& img, const grayPrefix& pre, int ox, int oy) const;

    // 内部数据（PrepareNCC 填充）。
    Rect rect_;
    std::vector<uint8_t> data_;  // w*h 模板像素拷贝
    std::vector<nccRun> runs_;
    double count_ = 0, sum_ = 0, variance_ = 0;
};

// PrepareNCC precomputes template statistics and contiguous mask runs. Mask
// origins/strides need not match the template; a smaller mask omits its missing
// pixels and all nonzero bytes are equally included (not weighted).
// templ 为空或 nil 时返回 nullptr。
std::shared_ptr<const PreparedNCC> PrepareNCC(const nt::Gray* templ, const nt::Gray* mask);

// coarseStep / refineStep：见 Go 注释。
int coarseStep(int tw, int th);
int refineStep(int step);

// newGrayPrefix covers columns [x0,x0+w) and rows [y0,y0+h) of img.
void newGrayPrefix(grayPrefix& g, const nt::Gray& img, int x0, int y0, int w, int h);

// ToGray 把采集帧（*image.RGBA，不预乘）转成灰度；输出原点 (0,0)。
// 子图（带父 stride、非零原点）按其自身像素转换。nil 返回 0x0 灰度图。
nt::Gray ToGray(const nt::RGBA* src);
// ToGray(*image.Gray)：复制，保留原点。
nt::Gray ToGray(const nt::Gray* src);
// ToGray(image.Image) 对解码 PNG 的通用路径：At().RGBA() 预乘后再算亮度。
// 等价于 Go 对 png.Decode 结果调用 match.ToGray。输出原点 (0,0)。
nt::Gray ToGrayAsset(const AssetImage& src);

// ScaleGray 双线性缩放到指定尺寸。nil 返回 0x0。
nt::Gray ScaleGray(const nt::Gray* src, int w, int h);

// CropRGBA 裁一块并复制成独立 RGBA（原点 0,0）。src 为 nil 返回 nil（默认构造）。
nt::RGBA CropRGBA(const nt::RGBA* src, const Rect& r);

}  // namespace nt::match
