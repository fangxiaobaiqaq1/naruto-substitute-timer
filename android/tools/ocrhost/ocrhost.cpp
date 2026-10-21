// tools/ocrhost/ocrhost.cpp — 主机侧检查安卓文字认边（nt/ocr.h、nt/hudtext.h；不进 APK）。
//
// 与 Go 的 android/tools/ocrcheck（Windows OCR 路径在 Linux 上跑）对照：
//
//   ocrhost [--names 白方小,…] [--interval 250ms] [--diagnose] [--no-text] <png 或目录>...
//
//  * 每帧走与 JNI 相同的 frame::Pipeline(cfg, text = true)（Gated 引擎 + 文字认边），采集时间按
//    --interval 递增；每个识别任务在下一帧之前做完（输出与本机速度无关）。打印 scene / 对局 /
//    布局 / 认边结果 / 文字状态，与 ocrcheck 的同名字段可比（我方名字是安卓新增的）。
//  * --diagnose：对每个 duel/camp 布局的帧，同步读一次两侧标题条，打印三种处理的原文
//    （格式同 ocrcheck --diagnose 的 views=[...]）与安卓的账号段，供逐行比较。
//  * --realtime MS：不等识别做完，每帧之间只睡 MS 毫秒（识别跨越多帧，像手机上那样）；
//    --job-delay MS：每个识别任务再多花 MS 毫秒（模拟慢手机，模拟器上一次 1–2 s）。
//  * 模型：仓库 internal/ocr/neuraldata/recognizer.onnx（与 Windows 内置、APK 内的同一文件）。
//
// 构建与运行：android/tools/ocrhost/run.sh [参数...]
#define main unused_hostcheck_main
#include "../hostcheck.cpp"
#undef main

#include <chrono>
#include <thread>

#include "nt/hudtext.h"
#include "nt/ocr.h"

namespace {

std::string quote(const std::string& s) {
    // Go %q for printable text: backslash-escape quotes and backslashes.
    std::string out = "\"";
    for (char c : s) {
        if (c == '"' || c == '\\') out.push_back('\\');
        out.push_back(c);
    }
    return out + "\"";
}

std::string views(const std::array<std::string, nt::hudtext::variants>& v) {
    std::string out = "[";
    for (size_t i = 0; i < v.size(); ++i) {
        if (i > 0) out += " ";
        out += quote(v[i]);
    }
    return out + "]";
}

}  // namespace

int main(int argc, char** argv) {
    std::string repo = REPO_ROOT;
    std::vector<std::string> names, files;
    long intervalMs = 250, realtimeMs = 0, jobDelayMs = 0;
    bool diagnose = false, text = true;
    for (int i = 1; i < argc; ++i) {
        const std::string a = argv[i];
        if (a == "--names" && i + 1 < argc) {
            std::string list = argv[++i], cur;
            for (size_t k = 0; k <= list.size(); ++k) {
                if (k == list.size() || list[k] == ',') {
                    if (!cur.empty()) names.push_back(cur);
                    cur.clear();
                } else {
                    cur.push_back(list[k]);
                }
            }
        } else if (a == "--interval" && i + 1 < argc) {
            intervalMs = std::strtol(argv[++i], nullptr, 10);
        } else if (a == "--realtime" && i + 1 < argc) {
            realtimeMs = std::strtol(argv[++i], nullptr, 10);
        } else if (a == "--job-delay" && i + 1 < argc) {
            jobDelayMs = std::strtol(argv[++i], nullptr, 10);
        } else if (a == "--diagnose") {
            diagnose = true;
        } else if (a == "--no-text") {
            text = false;
        } else if (isDir(a)) {
            for (const auto& n : listDir(a))
                if (endsWith(n, ".png")) files.push_back(a + "/" + n);
        } else {
            files.push_back(a);
        }
    }
    registerAssets(repo);
    const std::string modelPath = repo + "/internal/ocr/neuraldata/recognizer.onnx";
    nt::hudtext::Worker& worker = nt::hudtext::Worker::Instance();
    worker.SetLoader([modelPath](std::vector<uint8_t>& out, std::string* err) {
        if (!readFile(modelPath, out)) {
            if (err != nullptr) *err = "read " + modelPath;
            return false;
        }
        return true;
    });
    std::vector<uint8_t> model;
    readFile(modelPath, model);
    std::string err;
    std::unique_ptr<nt::ocr::Recognizer> sync = nt::ocr::Recognizer::Load(model.data(), model.size(), &err);
    if (sync == nullptr) {
        std::fprintf(stderr, "model: %s\n", err.c_str());
        return 1;
    }
    nt::config::Config cfg = nt::config::Default();
    cfg.UI.PlayerNames = names;
    nt::identity::SetMineNames(names);
    nt::frame::Pipeline pipeline(cfg, text);
    if (!pipeline.Ok()) {
        std::fprintf(stderr, "engine: %s\n", pipeline.Error().c_str());
        return 1;
    }
    std::atomic<int> jobs{0};
    std::vector<double> millis;
    std::mutex millisMu;
    if (nt::hudtext::Engine* t = pipeline.Text()) {
        t->OnRead = [&, jobDelayMs](nt::TimeNs, const std::array<std::array<std::string, nt::hudtext::variants>, 2>&, double ms) {
            if (jobDelayMs > 0) std::this_thread::sleep_for(std::chrono::milliseconds(jobDelayMs));
            std::lock_guard<std::mutex> lock(millisMu);
            millis.push_back(ms + static_cast<double>(jobDelayMs));
        };
    }
    nt::hudtext::NameLayout layout(cfg.Layout);
    int lazyMismatch = 0, lazyRows = 0, lazyJobs = 0;
    const nt::TimeNs base = 1'000'000'000'000LL;
    for (size_t i = 0; i < files.size(); ++i) {
        nt::RGBA img;
        if (!loadFrame(files[i], img)) {
            std::fprintf(stderr, "skip %s\n", files[i].c_str());
            continue;
        }
        const nt::TimeNs at = base + static_cast<nt::TimeNs>(i) * intervalMs * nt::Millisecond;
        nt::frame::Frame f = pipeline.Analyze(&img, at, at, nt::frame::MethodBuffer);
        if (realtimeMs > 0) {
            std::this_thread::sleep_for(std::chrono::milliseconds(realtimeMs));
        } else {
            // Let the background job (if any) finish before the next frame.
            for (int spin = 0; spin < 2000 && !worker.Idle(); ++spin) std::this_thread::sleep_for(std::chrono::milliseconds(2));
            std::this_thread::sleep_for(std::chrono::milliseconds(2));
        }
        const nt::engine::Result& r = f.Res;
        std::string base = files[i].substr(files[i].find_last_of('/') + 1);
        std::printf("%s\tscene=%s fight=%s unc=%s profile=%s side=%s mine=%s opp=%s text=%s\n", base.c_str(), r.Scene.c_str(),
                    r.Fighting ? "true" : "false", r.Uncertain ? "true" : "false", r.LayoutProfile.c_str(), r.PlayerSide.c_str(),
                    r.PlayerName.c_str(), r.OppName.c_str(), r.TextStatus.c_str());
        if (diagnose && (r.LayoutProfile == "duel" || r.LayoutProfile == "camp")) {
            std::array<nt::Rect, 2> regions;
            if (!layout.Regions(&img, r.LayoutProfile, regions)) {
                std::printf("\tdiagnose: unsupported OCR HUD geometry\n");
                continue;
            }
            std::array<nt::RGBA, 2> strips;
            for (size_t s = 0; s < 2; ++s) {
                strips[s] = nt::match::CropRGBA(&img, regions[s]);
                for (int y = 0; y < strips[s].Bounds().Dy(); ++y)
                    for (int x = 0; x < strips[s].Bounds().Dx(); ++x) strips[s].Pix[strips[s].PixOffset(x, y) + 3] = 255;
            }
            const auto rows = nt::hudtext::BuildRows(strips);
            std::vector<nt::ocr::Recognizer::RowImage> images;
            for (const auto& row : rows) images.push_back({&row.image, row.rect});
            std::vector<nt::ocr::Line> lines;
            if (!sync->ReadRows(images, lines, &err, 2)) {
                std::printf("\tdiagnose: %s\n", err.c_str());
                continue;
            }
            const auto texts = nt::hudtext::Texts(rows, lines);
            for (size_t s = 0; s < 2; ++s)
                std::printf("\t%c views=%s account=%s\n", "LR"[s], views(texts[s]).c_str(), nt::hudtext::Consensus(texts[s]).c_str());
            // The live job reads the third treatment only when it can matter: same values and evidence.
            nt::hudtext::ReadResult lazy;
            if (!nt::hudtext::ReadAccounts(*sync, strips, regions, lazy)) {
                std::printf("\tlazy: %s\n", lazy.err.c_str());
                lazyMismatch++;
                continue;
            }
            lazyRows += lazy.rows;
            lazyJobs++;
            for (size_t s = 0; s < 2; ++s) {
                const std::string full = nt::hudtext::Consensus(texts[s]);
                auto proof = full.empty() ? nullptr : nt::hudtext::Evidence(rows, lines, static_cast<int>(s), full, strips[s], regions[s]);
                const bool same = lazy.values[s] == full && (proof == nullptr) == (lazy.proofs[s] == nullptr) &&
                                  (proof == nullptr || (proof->bounds == lazy.proofs[s]->bounds && proof->cells.size() == lazy.proofs[s]->cells.size()));
                if (!same) {
                    lazyMismatch++;
                    std::printf("\t%c lazy mismatch: %s vs %s\n", "LR"[s], lazy.values[s].c_str(), full.c_str());
                }
            }
        }
    }
    double loadMs = 0;
    size_t weights = 0;
    worker.Stats(loadMs, weights);
    std::lock_guard<std::mutex> lock(millisMu);
    double sum = 0;
    for (double m : millis) sum += m;
    if (!millis.empty())
        std::fprintf(stderr, "ocr jobs %zu, mean %.1f ms per job (2 threads); model load %.1f ms, weights %zu bytes\n",
                     millis.size(), sum / millis.size(), loadMs, weights);
    if (diagnose)
        std::fprintf(stderr, "lazy third treatment: %d jobs, %d rows (of %d), %d mismatches\n", lazyJobs, lazyRows, lazyJobs * 6,
                     lazyMismatch);
    (void)jobs;
    return lazyMismatch == 0 ? 0 : 3;
}
