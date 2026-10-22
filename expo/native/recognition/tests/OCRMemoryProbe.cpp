// Host-only empirical lower bound, NOT an iOS extension footprint prediction.
#include "nt/ocr.h"
#include "nt/parallel.h"
#include <fstream>
#include <iostream>
#include <sys/resource.h>
#include <vector>
int main(int argc, char **argv) {
    if (argc < 2) { std::cerr << "usage: ocr_memory_probe recognizer.onnx [row-width=640]\n"; return 2; }
    nt::SetParallelEnabled(false);
    std::ifstream in(argv[1], std::ios::binary | std::ios::ate);
    if (!in) return 2;
    std::vector<uint8_t> bytes(size_t(in.tellg())); in.seekg(0);
    in.read(reinterpret_cast<char *>(bytes.data()), bytes.size());
    std::string error;
    auto model = nt::ocr::Recognizer::Load(bytes.data(), bytes.size(), &error);
    if (!model) { std::cerr << error << '\n'; return 1; }
    std::vector<uint8_t>().swap(bytes);
    const int width = argc > 2 ? std::stoi(argv[2]) : 640;
    nt::RGBA image = nt::RGBA::New(nt::MakeRect(0, 0, width, 48));
    // Match hudtext::ReadAccounts: two OCR rows may run concurrently even
    // when the core ParallelFor pool is disabled.
    std::vector<nt::ocr::Recognizer::RowImage> rows = {{&image, image.Bounds()}, {&image, image.Bounds()}};
    std::vector<nt::ocr::Line> lines;
    if (!model->ReadRows(rows, lines, &error, 2)) { std::cerr << error << '\n'; return 1; }
    rusage usage{}; getrusage(RUSAGE_SELF, &usage);
    std::cout << "width=" << width << " weightsMiB=" << model->WeightBytes() / 1048576.0
              << " concurrentRows=2"
              << " maxRSS_native_units=" << usage.ru_maxrss << " (Linux KiB; Darwin bytes)\n";
}
