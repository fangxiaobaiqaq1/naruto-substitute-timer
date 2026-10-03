// nt/assets.h — 资源仓库（architect 所有，实现在 src/core/assets.cpp）。
//
// C++ 从不自己解码 PNG。Kotlin（AssetLoader.kt）在启动时把 APK assets 里的每个
// PNG 解码成 *非预乘* RGBA8 并通过 JNI 注册进来；json 等文本按 UTF-8 字符串注册。
// key 是相对 assets 根目录的路径，正斜杠分隔，例如：
//   "templates/manifest.json"、"templates/raw_duel_round.png"
//   "avatars/index.json"、"avatars/90009.png"
//   "ninja_templates/hashirama.png"
//   "identity/<账号>.png"、"identity/seen/opp-*.png"、"identity/fight/*.png"（用户文件，来自 filesDir）
// 每个 PNG 还会附带一个文本 "<path>.sha256"：原始 PNG 文件字节的小写 SHA-256，
// 供 avatars/index.json 的完整性校验（Go loadAvatarCatalog 的 sha256 检查）。
//
// 线程安全：所有方法都加锁；image()/text() 返回共享所有权，调用方可长期持有。
#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

#include "nt/image.h"

namespace nt {

// 一张解码后的资源图：紧凑、非预乘 RGBA8，stride = 4*Width，原点 (0,0)。
struct AssetImage {
    int Width = 0;
    int Height = 0;
    std::vector<uint8_t> Pixels;  // 非预乘 R,G,B,A

    // 只读视图（Bounds = (0,0)-(W,H)）。注意：这是 *非预乘* 像素，
    // 转灰度必须用 match::ToGrayAsset（复刻 Go NRGBA 预乘路径），不能用 ToGray(RGBA)。
    RGBA View() const {
        return RGBA(const_cast<uint8_t*>(Pixels.data()), 4 * Width, MakeRect(0, 0, Width, Height));
    }
    // Go 通用路径 src.At(x,y).RGBA()，返回 16 位预乘分量（x,y 以 0 为原点）。
    void RGBA16(int x, int y, uint32_t& r, uint32_t& g, uint32_t& b, uint32_t& a) const {
        const uint8_t* p = Pixels.data() + (static_cast<size_t>(y) * Width + x) * 4;
        r = GoNRGBAChannel16(p[0], p[3]);
        g = GoNRGBAChannel16(p[1], p[3]);
        b = GoNRGBAChannel16(p[2], p[3]);
        a = GoAlpha16(p[3]);
    }
};

class AssetStore {
public:
    static AssetStore& Instance();

    void Clear();

    // rgba：非预乘 RGBA8，rowStride 为字节步长（>= 4*w）。会复制像素。
    void AddImage(const std::string& path, int w, int h, const uint8_t* rgba, size_t rowStride);
    void AddText(const std::string& path, std::string text);

    // 不存在时返回 nullptr。
    std::shared_ptr<const AssetImage> Image(const std::string& path) const;
    std::shared_ptr<const std::string> Text(const std::string& path) const;
    bool Has(const std::string& path) const;

    // 所有以 prefix 开头的 key（图片与文本），按字典序排序。递归（包含子目录）；
    // 需要 os.ReadDir 式「只列直接子文件」的调用方自己过滤掉含 '/' 的余部。
    std::vector<std::string> List(const std::string& prefix) const;

    // 每次 Add/Clear 递增；用于让缓存（例如 identity 名册）知道资源已变化。
    uint64_t Revision() const;

private:
    AssetStore() = default;
    mutable std::mutex mu_;
    std::map<std::string, std::shared_ptr<const AssetImage>> images_;
    std::map<std::string, std::shared_ptr<const std::string>> texts_;
    uint64_t revision_ = 0;
};

}  // namespace nt
