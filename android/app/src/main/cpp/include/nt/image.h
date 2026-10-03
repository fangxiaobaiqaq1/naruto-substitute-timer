// nt/image.h — Go "image" 包语义的最小 C++ 复刻（architect 所有）。
//
// 移植规则（所有 porter 必读）：
//  * Point / Rect 的运算与 Go image.Point / image.Rectangle 完全一致，
//    包括 Intersect 为空时返回零矩形 ZR、Inset 过窄时收缩到中点、
//    Rect::In 对空矩形恒为 true、Point::In 是半开区间。
//  * RGBA / Gray 是「带原点的视图」：pix 指向 rect.Min 处的像素（与 Go 的
//    Pix[0] 对应 Rect.Min 一致），stride 为字节步长。SubImage 与 Go 一样共享
//    像素、保留父图的 stride 与绝对坐标。
//  * 视图可以借用外部内存（AHardwareBuffer / 直接 ByteBuffer），也可以通过
//    owner 共享持有自己的内存（New / Clone）。复制视图是 O(1)。
//  * 默认构造的图像等价于 Go 的 nil / &RGBA{}：pix == nullptr，rect 为零。
//  * Go 函数参数 *image.RGBA / *image.Gray 在 C++ 中统一写成 const RGBA* /
//    const Gray*（nullptr == nil）；返回值统一按值返回。
//  * 分析流程绝不写入采集帧像素（视图里 pix 不是 const 只是为了让 New 出来的
//    缓冲区可写；来自采集的视图一律只读使用）。
#pragma once

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <cstring>
#include <memory>
#include <string>

namespace nt {

struct Rect;

struct Point {
    int X = 0;
    int Y = 0;

    constexpr Point() = default;
    constexpr Point(int x, int y) : X(x), Y(y) {}

    constexpr Point Add(Point q) const { return {X + q.X, Y + q.Y}; }
    constexpr Point Sub(Point q) const { return {X - q.X, Y - q.Y}; }
    constexpr Point Mul(int k) const { return {X * k, Y * k}; }
    constexpr bool operator==(Point o) const { return X == o.X && Y == o.Y; }
    constexpr bool operator!=(Point o) const { return !(*this == o); }
    // Point.In：半开区间 [Min, Max)。
    constexpr bool In(const Rect& r) const;
    std::string String() const { return "(" + std::to_string(X) + "," + std::to_string(Y) + ")"; }
};

constexpr Point Pt(int x, int y) { return Point(x, y); }

struct Rect {
    Point Min;
    Point Max;

    constexpr Rect() = default;
    constexpr Rect(Point mn, Point mx) : Min(mn), Max(mx) {}

    constexpr int Dx() const { return Max.X - Min.X; }
    constexpr int Dy() const { return Max.Y - Min.Y; }
    constexpr Point Size() const { return {Dx(), Dy()}; }
    constexpr bool Empty() const { return Min.X >= Max.X || Min.Y >= Max.Y; }
    constexpr Rect Add(Point p) const { return {Min.Add(p), Max.Add(p)}; }
    constexpr Rect Sub(Point p) const { return {Min.Sub(p), Max.Sub(p)}; }

    // Go: Inset 过窄的一维收缩到中点（整数除法向零截断）。
    constexpr Rect Inset(int n) const {
        Rect r = *this;
        if (r.Dx() < 2 * n) {
            r.Min.X = (r.Min.X + r.Max.X) / 2;
            r.Max.X = r.Min.X;
        } else {
            r.Min.X += n;
            r.Max.X -= n;
        }
        if (r.Dy() < 2 * n) {
            r.Min.Y = (r.Min.Y + r.Max.Y) / 2;
            r.Max.Y = r.Min.Y;
        } else {
            r.Min.Y += n;
            r.Max.Y -= n;
        }
        return r;
    }

    // Go: 交集为空时返回零矩形 ZR（不是退化矩形！）。
    constexpr Rect Intersect(const Rect& s) const {
        Rect r = *this;
        if (r.Min.X < s.Min.X) r.Min.X = s.Min.X;
        if (r.Min.Y < s.Min.Y) r.Min.Y = s.Min.Y;
        if (r.Max.X > s.Max.X) r.Max.X = s.Max.X;
        if (r.Max.Y > s.Max.Y) r.Max.Y = s.Max.Y;
        if (r.Empty()) return Rect{};
        return r;
    }

    // Go: Union（空矩形不参与）。
    constexpr Rect Union(const Rect& s) const {
        if (Empty()) return s;
        if (s.Empty()) return *this;
        Rect r = *this;
        if (r.Min.X > s.Min.X) r.Min.X = s.Min.X;
        if (r.Min.Y > s.Min.Y) r.Min.Y = s.Min.Y;
        if (r.Max.X < s.Max.X) r.Max.X = s.Max.X;
        if (r.Max.Y < s.Max.Y) r.Max.Y = s.Max.Y;
        return r;
    }

    // Go: Rectangle.In —— 空矩形恒在任何矩形内。
    constexpr bool In(const Rect& s) const {
        if (Empty()) return true;
        return s.Min.X <= Min.X && Max.X <= s.Max.X && s.Min.Y <= Min.Y && Max.Y <= s.Max.Y;
    }

    // Go: Rectangle.Eq —— 两个空矩形视为相等。注意 Go 代码里的 `==` / `!=`
    // 是逐字段比较（下面的 operator==），不是 Eq。
    constexpr bool Eq(const Rect& s) const { return (*this == s) || (Empty() && s.Empty()); }
    constexpr bool operator==(const Rect& o) const { return Min == o.Min && Max == o.Max; }
    constexpr bool operator!=(const Rect& o) const { return !(*this == o); }

    std::string String() const { return Min.String() + "-" + Max.String(); }
};

constexpr bool Point::In(const Rect& r) const {
    return r.Min.X <= X && X < r.Max.X && r.Min.Y <= Y && Y < r.Max.Y;
}

// Go image.Rect：自动把 min/max 规范化。
constexpr Rect MakeRect(int x0, int y0, int x1, int y1) {
    if (x0 > x1) { int t = x0; x0 = x1; x1 = t; }
    if (y0 > y1) { int t = y0; y0 = y1; y1 = t; }
    return Rect(Point(x0, y0), Point(x1, y1));
}

// color.RGBA 等价物（采集帧本身就是不透明 RGBA，没有预乘问题）。
struct Color {
    uint8_t R = 0, G = 0, B = 0, A = 0;
};

// image.RGBA 视图。像素顺序 R,G,B,A，每像素 4 字节。
struct RGBA {
    uint8_t* Pix = nullptr;  // 指向 Rect.Min 处像素
    int Stride = 0;          // 字节
    Rect Rect_;              // 用 Bounds() 访问；命名避免与类型 Rect 冲突
    std::shared_ptr<uint8_t> Owner;  // 可空：借用外部内存时为空

    RGBA() = default;
    RGBA(uint8_t* pix, int stride, const Rect& r, std::shared_ptr<uint8_t> owner = nullptr)
        : Pix(pix), Stride(stride), Rect_(r), Owner(std::move(owner)) {}

    // image.NewRGBA(r)：零初始化、紧凑 stride = 4*Dx。
    static RGBA New(const Rect& r) {
        RGBA img;
        img.Rect_ = r;
        int w = std::max(0, r.Dx()), h = std::max(0, r.Dy());
        img.Stride = 4 * w;
        size_t n = static_cast<size_t>(img.Stride) * static_cast<size_t>(h);
        if (n > 0) {
            std::shared_ptr<uint8_t> buf(new uint8_t[n](), std::default_delete<uint8_t[]>());
            img.Pix = buf.get();
            img.Owner = std::move(buf);
        }
        return img;
    }

    bool Nil() const { return Pix == nullptr && Rect_.Empty(); }
    const Rect& Bounds() const { return Rect_; }

    // Go: PixOffset 以 Rect.Min 为原点。
    ptrdiff_t PixOffset(int x, int y) const {
        return static_cast<ptrdiff_t>(y - Rect_.Min.Y) * Stride + static_cast<ptrdiff_t>(x - Rect_.Min.X) * 4;
    }
    const uint8_t* RowPtr(int y) const { return Pix + static_cast<ptrdiff_t>(y - Rect_.Min.Y) * Stride; }

    // Go: 越界返回 color.RGBA{}（全 0）。
    Color RGBAAt(int x, int y) const {
        if (!Point(x, y).In(Rect_)) return Color{};
        const uint8_t* p = Pix + PixOffset(x, y);
        return Color{p[0], p[1], p[2], p[3]};
    }
    void SetRGBA(int x, int y, Color c) {
        if (!Point(x, y).In(Rect_)) return;
        uint8_t* p = Pix + PixOffset(x, y);
        p[0] = c.R; p[1] = c.G; p[2] = c.B; p[3] = c.A;
    }

    // Go: SubImage 与父图共享像素与 stride；交集为空时返回 &RGBA{}。
    RGBA SubImage(const Rect& r0) const {
        Rect r = r0.Intersect(Rect_);
        if (r.Empty()) return RGBA{};
        return RGBA(Pix + PixOffset(r.Min.X, r.Min.Y), Stride, r, Owner);
    }

    // 深拷贝成紧凑、自持有的图（保留原点）。
    RGBA Clone() const {
        RGBA out = New(Rect_);
        int row = 4 * Rect_.Dx();
        for (int y = 0; y < Rect_.Dy(); ++y) std::memcpy(out.Pix + y * out.Stride, Pix + static_cast<ptrdiff_t>(y) * Stride, row);
        return out;
    }
};

// image.Gray 视图。
struct Gray {
    uint8_t* Pix = nullptr;
    int Stride = 0;
    Rect Rect_;
    std::shared_ptr<uint8_t> Owner;

    Gray() = default;
    Gray(uint8_t* pix, int stride, const Rect& r, std::shared_ptr<uint8_t> owner = nullptr)
        : Pix(pix), Stride(stride), Rect_(r), Owner(std::move(owner)) {}

    // image.NewGray(r)：零初始化、紧凑 stride = Dx。
    static Gray New(const Rect& r) {
        Gray img;
        img.Rect_ = r;
        int w = std::max(0, r.Dx()), h = std::max(0, r.Dy());
        img.Stride = w;
        size_t n = static_cast<size_t>(w) * static_cast<size_t>(h);
        if (n > 0) {
            std::shared_ptr<uint8_t> buf(new uint8_t[n](), std::default_delete<uint8_t[]>());
            img.Pix = buf.get();
            img.Owner = std::move(buf);
        }
        return img;
    }

    bool Nil() const { return Pix == nullptr && Rect_.Empty(); }
    const Rect& Bounds() const { return Rect_; }
    ptrdiff_t PixOffset(int x, int y) const {
        return static_cast<ptrdiff_t>(y - Rect_.Min.Y) * Stride + static_cast<ptrdiff_t>(x - Rect_.Min.X);
    }
    // Go: 越界返回 0。
    uint8_t GrayAt(int x, int y) const {
        if (!Point(x, y).In(Rect_)) return 0;
        return Pix[PixOffset(x, y)];
    }
    void SetGray(int x, int y, uint8_t v) {
        if (!Point(x, y).In(Rect_)) return;
        Pix[PixOffset(x, y)] = v;
    }
    Gray SubImage(const Rect& r0) const {
        Rect r = r0.Intersect(Rect_);
        if (r.Empty()) return Gray{};
        return Gray(Pix + PixOffset(r.Min.X, r.Min.Y), Stride, r, Owner);
    }
    Gray Clone() const {
        Gray out = New(Rect_);
        for (int y = 0; y < Rect_.Dy(); ++y) std::memcpy(out.Pix + y * out.Stride, Pix + static_cast<ptrdiff_t>(y) * Stride, Rect_.Dx());
        return out;
    }
};

// ---------------------------------------------------------------------------
// 解码后的 PNG 资源与 Go image.Decode 语义的桥接。
//
// AssetStore 中的图片是 *非预乘* RGBA8（Kotlin 用 inPremultiplied=false 解码，
// 并在解码前剥掉 iCCP/cHRM/gAMA/sRGB 块，保证与 Go 一样不做色彩管理）。
// Go 的 png 解码器对带 alpha 的 PNG 返回 image.NRGBA，其 At().RGBA() 返回
// *预乘* 的 16 位分量：r16 = (r8*0x101) * a8 / 0xff。下面的函数逐位复刻这一点；
// 对不透明像素它恒等于原 8 位值，因此对 RGB / 灰度 PNG 也成立。
// ---------------------------------------------------------------------------

// Go color.NRGBA.RGBA() 的单通道：返回 16 位预乘值。
constexpr uint32_t GoNRGBAChannel16(uint8_t c, uint8_t a) {
    return (static_cast<uint32_t>(c) * 0x101u) * static_cast<uint32_t>(a) / 0xffu;
}
// Go color.NRGBA.RGBA() 的 alpha：a | a<<8。
constexpr uint32_t GoAlpha16(uint8_t a) { return static_cast<uint32_t>(a) * 0x101u; }

// Go 通用路径（image.Image.At(...).RGBA() 再 >>8）的整数亮度：
// (299*r + 587*g + 114*b + 500) / 1000，r/g/b 为预乘 16 位值 >> 8。
constexpr uint8_t GoLumaFromNRGBA(uint8_t r, uint8_t g, uint8_t b, uint8_t a) {
    return static_cast<uint8_t>((299u * (GoNRGBAChannel16(r, a) >> 8) + 587u * (GoNRGBAChannel16(g, a) >> 8) +
                                 114u * (GoNRGBAChannel16(b, a) >> 8) + 500u) / 1000u);
}

// match.ToGray(*image.RGBA) 快速路径的亮度（采集帧，不预乘）。
constexpr uint8_t GoLumaRGBA(uint8_t r, uint8_t g, uint8_t b) {
    return static_cast<uint8_t>((299u * r + 587u * g + 114u * b + 500u) / 1000u);
}

}  // namespace nt
