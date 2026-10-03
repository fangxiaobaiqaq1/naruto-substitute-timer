// tools/hostcheck.cpp — 主机侧集成检查（不进 APK）。
//
// 把仓库资源按 ARCHITECTURE.md §5 的映射注册进 AssetStore（stb_image 解码，等价 Kotlin
// BitmapFactory 非预乘输出 + "<path>.sha256"），构建与 jni.cpp 相同的引擎，然后对给定的
// PNG 截图逐张分析并打印 scene / fighting / beads / ninjas。
//
//   hostcheck [--seq] <png 或目录>...
//     默认：每张图用全新引擎跑一次 AnalyzeAt（与 tools/gocheck 的 Go 驱动输出逐行可比）。
//     --seq：所有图共享一个 frame::Pipeline，按 100ms 间隔依次分析（覆盖去重/交付层）。
//
// 构建见 tools/hostcheck.sh。
#define STB_IMAGE_IMPLEMENTATION
#define STBI_ONLY_PNG
#include "stb_image.h"

#include <algorithm>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <dirent.h>
#include <string>
#include <sys/stat.h>
#include <vector>

#include "nt/assets.h"
#include "nt/config.h"
#include "nt/factory.h"
#include "nt/frame.h"
#include "nt/identity.h"
#include "nt/image.h"
#include "nt/ninja.h"
#include "nt/scene.h"

namespace {

// ---- SHA-256（仅主机工具：模拟 Kotlin 对原始 PNG 字节的哈希） ----
struct Sha256 {
    uint32_t h[8] = {0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19};
    static uint32_t rotr(uint32_t x, int n) { return (x >> n) | (x << (32 - n)); }
    void block(const uint8_t* p) {
        static const uint32_t k[64] = {
            0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
            0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
            0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
            0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
            0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
            0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
            0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
            0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};
        uint32_t w[64];
        for (int i = 0; i < 16; ++i) w[i] = (p[4 * i] << 24) | (p[4 * i + 1] << 16) | (p[4 * i + 2] << 8) | p[4 * i + 3];
        for (int i = 16; i < 64; ++i) {
            uint32_t s0 = rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >> 3);
            uint32_t s1 = rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >> 10);
            w[i] = w[i - 16] + s0 + w[i - 7] + s1;
        }
        uint32_t a = h[0], b = h[1], c = h[2], d = h[3], e = h[4], f = h[5], g = h[6], hh = h[7];
        for (int i = 0; i < 64; ++i) {
            uint32_t S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
            uint32_t ch = (e & f) ^ (~e & g);
            uint32_t t1 = hh + S1 + ch + k[i] + w[i];
            uint32_t S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
            uint32_t mj = (a & b) ^ (a & c) ^ (b & c);
            uint32_t t2 = S0 + mj;
            hh = g; g = f; f = e; e = d + t1; d = c; c = b; b = a; a = t1 + t2;
        }
        h[0] += a; h[1] += b; h[2] += c; h[3] += d; h[4] += e; h[5] += f; h[6] += g; h[7] += hh;
    }
    std::string hex(const std::vector<uint8_t>& data) {
        std::vector<uint8_t> m(data);
        uint64_t bits = static_cast<uint64_t>(data.size()) * 8;
        m.push_back(0x80);
        while (m.size() % 64 != 56) m.push_back(0);
        for (int i = 7; i >= 0; --i) m.push_back(static_cast<uint8_t>(bits >> (8 * i)));
        for (size_t i = 0; i < m.size(); i += 64) block(m.data() + i);
        char buf[65];
        for (int i = 0; i < 8; ++i) std::snprintf(buf + 8 * i, 9, "%08x", h[i]);
        return std::string(buf, 64);
    }
};

bool readFile(const std::string& path, std::vector<uint8_t>& out) {
    FILE* f = std::fopen(path.c_str(), "rb");
    if (!f) return false;
    std::fseek(f, 0, SEEK_END);
    long n = std::ftell(f);
    std::fseek(f, 0, SEEK_SET);
    out.resize(n > 0 ? static_cast<size_t>(n) : 0);
    size_t got = n > 0 ? std::fread(out.data(), 1, out.size(), f) : 0;
    std::fclose(f);
    return got == out.size();
}

bool endsWith(const std::string& s, const std::string& suf) {
    return s.size() >= suf.size() && s.compare(s.size() - suf.size(), suf.size(), suf) == 0;
}

bool isDir(const std::string& p) {
    struct stat st{};
    return stat(p.c_str(), &st) == 0 && S_ISDIR(st.st_mode);
}

std::vector<std::string> listDir(const std::string& dir) {
    std::vector<std::string> out;
    DIR* d = opendir(dir.c_str());
    if (!d) return out;
    while (dirent* e = readdir(d)) {
        std::string n = e->d_name;
        if (n == "." || n == "..") continue;
        out.push_back(n);
    }
    closedir(d);
    std::sort(out.begin(), out.end());
    return out;
}

int g_images = 0, g_texts = 0;

void addPNG(const std::string& file, const std::string& key) {
    std::vector<uint8_t> raw;
    if (!readFile(file, raw)) {
        std::fprintf(stderr, "read %s failed\n", file.c_str());
        return;
    }
    int w = 0, h = 0, n = 0;
    uint8_t* px = stbi_load_from_memory(raw.data(), static_cast<int>(raw.size()), &w, &h, &n, 4);
    if (!px) {
        std::fprintf(stderr, "decode %s failed: %s\n", file.c_str(), stbi_failure_reason());
        return;
    }
    nt::AssetStore::Instance().AddText(key + ".sha256", Sha256{}.hex(raw));
    nt::AssetStore::Instance().AddImage(key, w, h, px, static_cast<size_t>(4 * w));
    stbi_image_free(px);
    g_images++;
}

void addText(const std::string& file, const std::string& key) {
    std::vector<uint8_t> raw;
    if (!readFile(file, raw)) return;
    nt::AssetStore::Instance().AddText(key, std::string(raw.begin(), raw.end()));
    g_texts++;
}

// ARCHITECTURE.md §5 / app/build.gradle.kts SyncRecognitionAssets 的同一映射。
void registerAssets(const std::string& repo) {
    for (const auto& n : listDir(repo + "/assets/templates")) {
        if (endsWith(n, ".png")) addPNG(repo + "/assets/templates/" + n, "templates/" + n);
        if (n == "manifest.json") addText(repo + "/assets/templates/" + n, "templates/" + n);
    }
    for (const auto& n : listDir(repo + "/assets/avatars")) {
        if (endsWith(n, ".png")) addPNG(repo + "/assets/avatars/" + n, "avatars/" + n);
        if (n == "index.json") addText(repo + "/assets/avatars/" + n, "avatars/" + n);
    }
    for (const auto& n : listDir(repo + "/assets/game")) {
        if (endsWith(n, ".json")) addText(repo + "/assets/game/" + n, "game/" + n);
    }
    for (const auto& n : listDir(repo + "/internal/ninja/templates")) {
        if (endsWith(n, ".png") && !endsWith(n, ".source.png"))
            addPNG(repo + "/internal/ninja/templates/" + n, "ninja_templates/" + n);
    }
}

// Go 测试的读图方式：png.Decode + draw.Draw 到 *image.RGBA（NRGBA → 预乘）。
bool loadFrame(const std::string& path, nt::RGBA& out) {
    std::vector<uint8_t> raw;
    if (!readFile(path, raw)) return false;
    int w = 0, h = 0, n = 0;
    uint8_t* px = stbi_load_from_memory(raw.data(), static_cast<int>(raw.size()), &w, &h, &n, 4);
    if (!px) return false;
    out = nt::RGBA::New(nt::MakeRect(0, 0, w, h));
    for (int i = 0; i < w * h; ++i) {
        const uint8_t* p = px + 4 * i;
        uint8_t* q = out.Pix + 4 * i;
        q[0] = static_cast<uint8_t>(nt::GoNRGBAChannel16(p[0], p[3]) >> 8);
        q[1] = static_cast<uint8_t>(nt::GoNRGBAChannel16(p[1], p[3]) >> 8);
        q[2] = static_cast<uint8_t>(nt::GoNRGBAChannel16(p[2], p[3]) >> 8);
        q[3] = p[3];
    }
    stbi_image_free(px);
    return true;
}

std::string beadsString(const std::vector<nt::engine::BeadInfo>& beads) {
    std::string s;
    for (const auto& b : beads) {
        char st = b.Unknown ? 'U' : (b.Gold ? 'G' : (b.Lit ? 'L' : 'D'));
        char buf[64];
        std::snprintf(buf, sizeof buf, "%s%s=%c(%.3f)@%d,%d", s.empty() ? "" : " ", b.Label.c_str(), st, b.Conf, b.X, b.Y);
        s += buf;
    }
    return s;
}

void printResult(const std::string& name, const nt::engine::Result& r) {
    std::printf("%s\tfight=%d unc=%d scene=%s gate=%.4f profile=%s open=%d slots=%d/%d L=%s R=%s side=%s opp=%s name=%s\n\tbeads: %s\n",
                name.c_str(), r.Fighting, r.Uncertain, r.Scene.c_str(), r.GateScore, r.LayoutProfile.c_str(),
                r.RoundOpening, r.LeftSlots, r.RightSlots, r.LeftNinja.c_str(), r.RightNinja.c_str(),
                r.PlayerSide.c_str(), r.OppName.c_str(), r.Name.c_str(), beadsString(r.Beads).c_str());
}

}  // namespace

int main(int argc, char** argv) {
    std::string repo = "/ssd/work/naruto-substitute-timer-v0.2.8";
    bool seq = false;
    std::vector<std::string> inputs;
    for (int i = 1; i < argc; ++i) {
        std::string a = argv[i];
        if (a == "--seq") {
            seq = true;
        } else if (a == "--repo" && i + 1 < argc) {
            repo = argv[++i];
        } else {
            inputs.push_back(a);
        }
    }
    registerAssets(repo);
    std::fprintf(stderr, "assets: %d images, %d texts\n", g_images, g_texts);

    nt::config::Config cfg = nt::config::Default();
    nt::identity::SetMineNames(cfg.UI.PlayerNames);

    std::string err;
    std::string desc;
    {
        auto gate = nt::scene::NewGate(cfg, &desc, &err);
        std::fprintf(stderr, "gate: %s %s\n", desc.c_str(), err.c_str());
    }
    nt::ninja::AvatarCatalogStats stats;
    if (nt::ninja::AvatarStats(stats, &err)) {
        std::fprintf(stderr, "avatars: entries=%d base=%d skins=%d\n", stats.Entries, stats.Base, stats.Skins);
    } else {
        std::fprintf(stderr, "avatars: FAILED %s\n", err.c_str());
    }

    std::vector<std::string> files;
    for (const auto& in : inputs) {
        if (isDir(in)) {
            for (const auto& n : listDir(in))
                if (endsWith(n, ".png")) files.push_back(in + "/" + n);
        } else {
            files.push_back(in);
        }
    }

    const nt::TimeNs base = 1700000000LL * nt::Second;
    std::unique_ptr<nt::frame::Pipeline> pipe;
    if (seq) {
        pipe = std::make_unique<nt::frame::Pipeline>(cfg);
        if (!pipe->Ok()) {
            std::fprintf(stderr, "pipeline: %s\n", pipe->Error().c_str());
            return 1;
        }
    }
    int idx = 0;
    for (const auto& f : files) {
        nt::RGBA img;
        if (!loadFrame(f, img)) {
            std::fprintf(stderr, "skip %s (decode)\n", f.c_str());
            continue;
        }
        std::string name = f.substr(f.find_last_of('/') + 1);
        nt::TimeNs at = base + static_cast<nt::TimeNs>(idx++) * 100 * nt::Millisecond;
        if (seq) {
            nt::frame::Frame fr = pipe->Analyze(&img, at, at, nt::frame::MethodBuffer);
            printResult(name, fr.Res);
            std::printf("\tseq=%llu dup=%d reused=%d hold=%d err=%s status=%s\n",
                        static_cast<unsigned long long>(fr.Sequence), fr.Duplicate, fr.Reused, fr.Hold,
                        fr.Err.c_str(), fr.Status.c_str());
        } else {
            std::string e;
            auto eng = nt::factory::New(nt::factory::FromApp(cfg), &e);
            if (!eng) {
                std::fprintf(stderr, "engine: %s\n", e.c_str());
                return 1;
            }
            printResult(name, eng->AnalyzeAt(&img, base));
        }
        std::fflush(stdout);
    }
    return 0;
}
