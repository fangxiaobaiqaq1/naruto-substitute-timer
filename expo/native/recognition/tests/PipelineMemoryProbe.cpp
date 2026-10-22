// Linux measurement tool only: reuse the existing Android host asset decoder.
// This measures the actual pipeline + shared HUD OCR, not an iOS footprint.
#define main unused_android_hostcheck_main
#include "../../../../android/tools/hostcheck.cpp"
#undef main
#include "nt/hudtext.h"
#include "nt/ocr.h"
#include <iostream>
#include <sys/resource.h>
int main(int argc, char **argv) {
    if (argc != 3) { std::cerr << "usage: pipeline_memory_probe REPO FRAME.png\n"; return 2; }
    nt::SetParallelEnabled(false);
    registerAssets(argv[1]);
    nt::RGBA image;
    if (!loadFrame(argv[2], image)) return 2;
    if (image.Bounds().Dx() > 1280) {
        auto scaled = nt::RGBA::New(nt::MakeRect(0, 0, 1280, int(std::round(1280. * image.Bounds().Dy() / image.Bounds().Dx()))));
        nt::ocr::ScaleBiLinear(scaled, image, image.Bounds());
        image = std::move(scaled);
    }
    auto cfg = nt::config::Default();
    nt::frame::Pipeline pipeline(cfg);
    if (!pipeline.Ok()) { std::cerr << pipeline.Error() << '\n'; return 1; }
    auto at = nt::NowNs();
    auto f = pipeline.Analyze(&image, at, at, "ios-memory-probe");
    std::vector<uint8_t> bytes;
    if (!readFile(std::string(argv[1]) + "/internal/ocr/neuraldata/recognizer.onnx", bytes)) return 2;
    std::string error;
    auto model = nt::ocr::Recognizer::Load(bytes.data(), bytes.size(), &error);
    std::vector<uint8_t>().swap(bytes);
    if (!model) { std::cerr << error << '\n'; return 1; }
    nt::hudtext::NameLayout layout(cfg.Layout);
    std::array<nt::Rect, 2> regions;
    if (!layout.Regions(&image, "duel", regions)) return 2;
    std::array<nt::RGBA, 2> strips;
    for (int i = 0; i < 2; ++i) strips[i] = nt::match::CropRGBA(&image, regions[i]);
    nt::hudtext::ReadResult result;
    if (!nt::hudtext::ReadAccounts(*model, strips, regions, result)) { std::cerr << result.err << '\n'; return 1; }
    rusage usage{}; getrusage(RUSAGE_SELF, &usage);
    std::cout << "scene=" << f.Res.Scene << " profile=" << f.Res.LayoutProfile
              << " frame=" << image.Bounds().Dx() << 'x' << image.Bounds().Dy()
              << " OCRrows=" << result.rows << " left=" << result.values[0] << " right=" << result.values[1]
              << " maxRSS_KiB=" << usage.ru_maxrss << '\n';
}
