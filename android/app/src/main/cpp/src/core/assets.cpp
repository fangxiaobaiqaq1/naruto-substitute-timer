// AssetStore + 进程哈希种子（architect 所有）。
#include "nt/assets.h"

#include <chrono>
#include <algorithm>
#include <cstring>
#include <random>

#include "nt/hash.h"

namespace nt {

AssetStore& AssetStore::Instance() {
    static AssetStore store;
    return store;
}

void AssetStore::Clear() {
    std::lock_guard<std::mutex> lock(mu_);
    images_.clear();
    texts_.clear();
    revision_++;
}

void AssetStore::AddImage(const std::string& path, int w, int h, const uint8_t* rgba, size_t rowStride) {
    if (w < 0 || h < 0 || (w * h > 0 && rgba == nullptr)) return;
    auto img = std::make_shared<AssetImage>();
    img->Width = w;
    img->Height = h;
    size_t row = static_cast<size_t>(w) * 4;
    if (rowStride < row) rowStride = row;
    img->Pixels.resize(row * static_cast<size_t>(h));
    for (int y = 0; y < h; ++y) {
        std::memcpy(img->Pixels.data() + row * y, rgba + rowStride * y, row);
    }
    std::lock_guard<std::mutex> lock(mu_);
    images_[path] = std::move(img);
    revision_++;
}

void AssetStore::AddText(const std::string& path, std::string text) {
    auto t = std::make_shared<const std::string>(std::move(text));
    std::lock_guard<std::mutex> lock(mu_);
    texts_[path] = std::move(t);
    revision_++;
}

std::shared_ptr<const AssetImage> AssetStore::Image(const std::string& path) const {
    std::lock_guard<std::mutex> lock(mu_);
    auto it = images_.find(path);
    return it == images_.end() ? nullptr : it->second;
}

std::shared_ptr<const std::string> AssetStore::Text(const std::string& path) const {
    std::lock_guard<std::mutex> lock(mu_);
    auto it = texts_.find(path);
    return it == texts_.end() ? nullptr : it->second;
}

bool AssetStore::Has(const std::string& path) const {
    std::lock_guard<std::mutex> lock(mu_);
    return images_.count(path) > 0 || texts_.count(path) > 0;
}

std::vector<std::string> AssetStore::List(const std::string& prefix) const {
    std::vector<std::string> out;
    std::lock_guard<std::mutex> lock(mu_);
    for (auto it = images_.lower_bound(prefix); it != images_.end() && it->first.compare(0, prefix.size(), prefix) == 0; ++it)
        out.push_back(it->first);
    for (auto it = texts_.lower_bound(prefix); it != texts_.end() && it->first.compare(0, prefix.size(), prefix) == 0; ++it)
        out.push_back(it->first);
    std::sort(out.begin(), out.end());
    return out;
}

uint64_t AssetStore::Revision() const {
    std::lock_guard<std::mutex> lock(mu_);
    return revision_;
}

uint64_t ProcessHashSeed() {
    static const uint64_t seed = [] {
        std::random_device rd;
        uint64_t s = (static_cast<uint64_t>(rd()) << 32) ^ rd();
        s ^= static_cast<uint64_t>(std::chrono::steady_clock::now().time_since_epoch().count());
        return s;
    }();
    return seed;
}

}  // namespace nt
