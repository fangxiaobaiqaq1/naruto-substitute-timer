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
// 内存（docs/性能优化-android-native.md）：识别核心对资源图只用到 Go 语义的亮度（ToGrayAsset）与
// alpha >= 128 掩膜（头像）。AddImage 当场算好这两样并丢掉 RGBA：每像素 4 字节 → 1 字节
// （+ 有透明像素时 1 位）。亮度只算一次，引擎构建 / 校准重建直接共享，不再从 RGBA 重算。
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

// 一张资源图（原点 (0,0)）。注册时从非预乘 RGBA8 转换，之后不可变。
struct AssetImage {
    int Width = 0;
    int Height = 0;
    // Luma 是 Go 对 png.Decode 结果调用 match.ToGray 的灰度：NRGBA 预乘 16 位分量 >> 8 后的
    // 整数亮度（GoLumaFromNRGBA；不透明像素即 8 位原值的亮度）。紧凑 stride = Width。
    // 只读：复制视图共享同一块像素（match::ToGrayAsset 就返回它）。
    Gray Luma;
    // alpha >= 128 的像素位图（Go: color.NRGBA.RGBA() 的 a16 >= 0x8000），行优先，每行
    // RowWords() 个 64 位字。空 = 所有像素都满足（例如不透明 PNG）。
    std::vector<uint64_t> OpaqueBits;

    size_t RowWords() const { return (static_cast<size_t>(Width) + 63) / 64; }
    // Opaque：(x, y) 的 alpha >= 128（x,y 以 0 为原点且在范围内）。
    bool Opaque(int x, int y) const {
        if (OpaqueBits.empty()) return true;
        const uint64_t word = OpaqueBits[static_cast<size_t>(y) * RowWords() + static_cast<size_t>(x) / 64];
        return ((word >> (static_cast<unsigned>(x) % 64)) & 1u) != 0;
    }
    // 常驻像素字节（诊断）。
    size_t Bytes() const {
        return static_cast<size_t>(Width) * static_cast<size_t>(Height) + OpaqueBits.size() * sizeof(uint64_t);
    }
};

class AssetStore {
public:
    static AssetStore& Instance();

    void Clear();

    // rgba：非预乘 RGBA8，rowStride 为字节步长（>= 4*w）。转换成 AssetImage（不保留 rgba）。
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
