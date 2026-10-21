// Owner: ocr（安卓本机文字识别，见 nt/ocr.h）
// Port of: internal/ocr/local_rows.go（recognizeRequest / recognizeRow / decodeCharacters / sessionCharacters）
// 以及 golang.org/x/image/draw 的 BiLinear（Kernel 缩放）与 NearestNeighbor 对 *image.RGBA 的 Src 路径。
//
// 预处理和解码全是确定的整数/双精度运算，逐位对应 Go（本文件不允许乘加融合，见 CMakeLists.txt）。
#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstring>
#include <thread>

#include "nt/ocr.h"
#include "nt/ocr_net.h"

namespace nt::ocr {

namespace {

bool fail(std::string* err, const std::string& msg) {
    if (err != nullptr) *err = msg;
    return false;
}

// x/image/draw scale.go: source / contrib / distrib / newDistrib with BiLinear
// (&Kernel{1, func(t) { return 1 - t }}).
struct Source {
    int32_t i = 0, j = 0;
    double invTotalWeight = 0, invTotalWeightFFFF = 0;
};
struct Contrib {
    int32_t coord = 0;
    double weight = 0;
};
struct Distrib {
    std::vector<Source> sources;
    std::vector<Contrib> contribs;
};

Distrib newDistrib(int32_t dw, int32_t sw) {
    const double scale = static_cast<double>(sw) / static_cast<double>(dw);
    double halfWidth = 1.0, kernelArgScale = 1.0;
    if (scale > 1) {
        halfWidth *= scale;
        kernelArgScale = 1 / scale;
    }
    Distrib d;
    d.sources.resize(static_cast<size_t>(dw));
    int32_t n = 0;
    for (int32_t x = 0; x < dw; ++x) {
        const double center = ((static_cast<double>(x) + 0.5) * scale) - 0.5;
        int32_t i = static_cast<int32_t>(std::floor(center - halfWidth));
        if (i < 0) i = 0;
        int32_t j = static_cast<int32_t>(std::ceil(center + halfWidth));
        if (j > sw) {
            j = sw;
            if (j < i) j = i;
        }
        d.sources[static_cast<size_t>(x)] = Source{i, j, center, 0};
        n += j - i;
    }
    d.contribs.reserve(static_cast<size_t>(std::max<int32_t>(0, n)));
    for (Source& b : d.sources) {
        double totalWeight = 0.0;
        const int32_t l = static_cast<int32_t>(d.contribs.size());
        for (int32_t coord = b.i; coord < b.j; ++coord) {
            double t = (b.invTotalWeight - static_cast<double>(coord)) * kernelArgScale;
            if (t < 0) t = -t;
            if (t >= 1.0) continue;
            const double weight = 1 - t;
            if (weight == 0) continue;
            totalWeight += weight;
            d.contribs.push_back(Contrib{coord, weight});
        }
        totalWeight = 1 / totalWeight;
        b = Source{l, static_cast<int32_t>(d.contribs.size()), totalWeight, totalWeight / 0xffff};
    }
    return d;
}

uint16_t ftou(double f) {
    const int32_t i = static_cast<int32_t>((0xffff * f) + 0.5);
    if (i > 0xffff) return 0xffff;
    if (i > 0) return static_cast<uint16_t>(i);
    return 0;
}

void splitLines(const std::string& dictionary, std::vector<std::string>& out) {
    // strings.TrimRight(dictionary, "\r\n") then strings.Split(…, "\n").
    size_t end = dictionary.size();
    while (end > 0 && (dictionary[end - 1] == '\r' || dictionary[end - 1] == '\n')) --end;
    size_t start = 0;
    for (;;) {
        const size_t nl = dictionary.find('\n', start);
        if (nl == std::string::npos || nl >= end) {
            out.push_back(dictionary.substr(start, end - start));
            return;
        }
        out.push_back(dictionary.substr(start, nl - start));
        start = nl + 1;
    }
}

}  // namespace

void ScaleBiLinear(RGBA& dst, const RGBA& src, const Rect& sr) {
    const int32_t dw = dst.Bounds().Dx(), dh = dst.Bounds().Dy(), sw = sr.Dx(), sh = sr.Dy();
    if (dw <= 0 || dh <= 0 || sw <= 0 || sh <= 0) return;
    const Distrib horizontal = newDistrib(dw, sw), vertical = newDistrib(dh, sh);
    // scaleX_RGBA
    std::vector<double> tmp(static_cast<size_t>(dw) * sh * 4);
    size_t t = 0;
    for (int32_t y = 0; y < sh; ++y) {
        const uint8_t* row = src.RowPtr(sr.Min.Y + y);
        for (const Source& s : horizontal.sources) {
            double pr = 0, pg = 0, pb = 0, pa = 0;
            for (int32_t k = s.i; k < s.j; ++k) {
                const Contrib& c = horizontal.contribs[static_cast<size_t>(k)];
                const uint8_t* p = row + static_cast<ptrdiff_t>(sr.Min.X + c.coord - src.Bounds().Min.X) * 4;
                pr += static_cast<double>(static_cast<uint32_t>(p[0]) * 0x101) * c.weight;
                pg += static_cast<double>(static_cast<uint32_t>(p[1]) * 0x101) * c.weight;
                pb += static_cast<double>(static_cast<uint32_t>(p[2]) * 0x101) * c.weight;
                pa += static_cast<double>(static_cast<uint32_t>(p[3]) * 0x101) * c.weight;
            }
            tmp[t * 4 + 0] = pr * s.invTotalWeightFFFF;
            tmp[t * 4 + 1] = pg * s.invTotalWeightFFFF;
            tmp[t * 4 + 2] = pb * s.invTotalWeightFFFF;
            tmp[t * 4 + 3] = pa * s.invTotalWeightFFFF;
            ++t;
        }
    }
    // scaleY_RGBA_Src
    for (int32_t dx = 0; dx < dw; ++dx) {
        for (int32_t dy = 0; dy < dh; ++dy) {
            const Source& s = vertical.sources[static_cast<size_t>(dy)];
            double pr = 0, pg = 0, pb = 0, pa = 0;
            for (int32_t k = s.i; k < s.j; ++k) {
                const Contrib& c = vertical.contribs[static_cast<size_t>(k)];
                const double* p = &tmp[(static_cast<size_t>(c.coord) * dw + dx) * 4];
                pr += p[0] * c.weight;
                pg += p[1] * c.weight;
                pb += p[2] * c.weight;
                pa += p[3] * c.weight;
            }
            if (pr > pa) pr = pa;
            if (pg > pa) pg = pa;
            if (pb > pa) pb = pa;
            uint8_t* d = dst.Pix + dst.PixOffset(dst.Bounds().Min.X + dx, dst.Bounds().Min.Y + dy);
            d[0] = static_cast<uint8_t>(ftou(pr * s.invTotalWeight) >> 8);
            d[1] = static_cast<uint8_t>(ftou(pg * s.invTotalWeight) >> 8);
            d[2] = static_cast<uint8_t>(ftou(pb * s.invTotalWeight) >> 8);
            d[3] = static_cast<uint8_t>(ftou(pa * s.invTotalWeight) >> 8);
        }
    }
}

void ScaleNearest(RGBA& dst, const Rect& dr, const RGBA& src) {
    const Rect sr = src.Bounds();
    const Rect adr = dst.Bounds().Intersect(dr).Sub(dr.Min);
    if (adr.Empty() || sr.Empty()) return;
    if (dr.Size() == sr.Size()) {  // nnInterpolator.Scale → Copy
        for (int y = adr.Min.Y; y < adr.Max.Y; ++y)
            std::memcpy(dst.Pix + dst.PixOffset(dr.Min.X + adr.Min.X, dr.Min.Y + y), src.RowPtr(sr.Min.Y + y) + adr.Min.X * 4,
                        static_cast<size_t>(adr.Dx()) * 4);
        return;
    }
    const uint64_t dw2 = static_cast<uint64_t>(dr.Dx()) * 2, dh2 = static_cast<uint64_t>(dr.Dy()) * 2;
    const uint64_t sw = static_cast<uint64_t>(sr.Dx()), sh = static_cast<uint64_t>(sr.Dy());
    for (int32_t dy = adr.Min.Y; dy < adr.Max.Y; ++dy) {
        const uint64_t sy = (2 * static_cast<uint64_t>(dy) + 1) * sh / dh2;
        const uint8_t* srow = src.RowPtr(sr.Min.Y + static_cast<int>(sy));
        uint8_t* d = dst.Pix + dst.PixOffset(dr.Min.X + adr.Min.X, dr.Min.Y + dy);
        for (int32_t dx = adr.Min.X; dx < adr.Max.X; ++dx, d += 4) {
            const uint64_t sx = (2 * static_cast<uint64_t>(dx) + 1) * sw / dw2;
            std::memcpy(d, srow + sx * 4, 4);
        }
    }
}

Recognizer::~Recognizer() = default;

std::unique_ptr<Recognizer> Recognizer::Load(const uint8_t* data, size_t size, std::string* err) {
    std::unique_ptr<Recognizer> r(new Recognizer());
    r->net_ = std::make_unique<Net>();
    if (!r->net_->Parse(data, size, err)) return nullptr;
    if (r->net_->dictionary.empty()) {
        fail(err, "OCR model character dictionary unavailable");
        return nullptr;
    }
    // Go sessionCharacters: blank, the dictionary lines, then " ".
    r->characters_.push_back("");
    splitLines(r->net_->dictionary, r->characters_);
    r->characters_.push_back(" ");
    r->net_->dictionary.clear();
    r->net_->dictionary.shrink_to_fit();
    return r;
}

size_t Recognizer::WeightBytes() const { return net_ != nullptr ? net_->WeightBytes() : 0; }

bool Recognizer::Run(const float* input, int width, std::vector<float>& out, int& steps, int& classes,
                     std::string* err, const std::atomic<bool>* cancel) const {
    std::vector<int64_t> shape;
    if (!net_->Run(input, width, out, shape, err, cancel)) return false;
    if (shape.size() != 3 || shape[0] != 1 || shape[2] != static_cast<int64_t>(characters_.size()))
        return fail(err, "invalid OCR output shape, dictionary " + std::to_string(characters_.size()));
    steps = static_cast<int>(shape[1]);
    classes = static_cast<int>(shape[2]);
    return true;
}

// Go recognizeRow: BiLinear to height 48, then BGR planes of c/127.5 − 1.
std::vector<float> Recognizer::RowInput(const RGBA& img, const Rect& region, int& width) {
    width = static_cast<int>(std::ceil(static_cast<double>(region.Dx()) * 48 / static_cast<double>(region.Dy())));
    width = std::max(16, std::min(1600, width));
    RGBA resized = RGBA::New(MakeRect(0, 0, width, 48));
    ScaleBiLinear(resized, img, region);
    const size_t plane = static_cast<size_t>(width) * 48;
    std::vector<float> input(plane * 3);
    for (int y = 0; y < 48; ++y) {
        const uint8_t* row = resized.RowPtr(y);
        for (int x = 0; x < width; ++x) {
            const uint8_t* c = row + x * 4;
            const size_t p = static_cast<size_t>(y) * width + x;
            input[p] = static_cast<float>(c[2]) / 127.5f - 1;
            input[plane + p] = static_cast<float>(c[1]) / 127.5f - 1;
            input[2 * plane + p] = static_cast<float>(c[0]) / 127.5f - 1;
        }
    }
    return input;
}

bool Recognizer::ReadRow(const RGBA& img, const Rect& region, Line& out, std::string* err) const {
    int width = 0;
    const std::vector<float> input = RowInput(img, region, width);
    std::vector<float> values;
    int steps = 0, classes = 0;
    if (!Run(input.data(), width, values, steps, classes, err)) return false;
    out = DecodeCharacters(values.data(), steps, characters_, region);
    return true;
}

bool Recognizer::ReadRegions(const RGBA& img, const std::vector<Rect>& regions0, std::vector<Line>& out, std::string* err,
                             int threads) const {
    std::vector<Rect> regions = regions0;
    if (regions.empty()) regions.push_back(img.Bounds());
    if (regions.size() > 8) return fail(err, "too many OCR rows");
    for (const Rect& r : regions)
        if (r.Empty() || !r.In(img.Bounds())) return fail(err, "invalid OCR row");
    std::vector<Line> lines(regions.size());
    std::vector<std::string> errors(regions.size());
    std::vector<char> ok(regions.size(), 0);
    auto work = [&](size_t first, size_t step) {
        for (size_t k = first; k < regions.size(); k += step) ok[k] = ReadRow(img, regions[k], lines[k], &errors[k]) ? 1 : 0;
    };
    const size_t workers = std::max<size_t>(1, std::min<size_t>(static_cast<size_t>(std::max(1, threads)), regions.size()));
    std::vector<std::thread> helpers;
    for (size_t w = 1; w < workers; ++w) helpers.emplace_back(work, w, workers);
    work(0, workers);
    for (std::thread& h : helpers) h.join();
    out.clear();
    for (size_t k = 0; k < regions.size(); ++k) {
        if (!ok[k]) return fail(err, errors[k]);
        if (!lines[k].Words.empty()) out.push_back(std::move(lines[k]));
    }
    return true;
}

bool Recognizer::ReadRows(const std::vector<RowImage>& rows, std::vector<Line>& out, std::string* err, int threads,
                          const std::atomic<bool>* cancel, std::vector<double>* rowMillis) const {
    if (rows.size() > 8) return fail(err, "too many OCR rows");
    for (const RowImage& r : rows)
        if (r.image == nullptr || r.image->Bounds().Empty() || r.rect.Empty()) return fail(err, "invalid OCR row");
    std::vector<Line> lines(rows.size());
    std::vector<std::string> errors(rows.size());
    std::vector<char> ok(rows.size(), 0);
    std::vector<double> millis(rows.size(), 0);
    auto work = [&](size_t first, size_t step) {
        for (size_t k = first; k < rows.size(); k += step) {
            const auto start = std::chrono::steady_clock::now();
            int width = 0;
            const std::vector<float> input = RowInput(*rows[k].image, rows[k].image->Bounds(), width);
            std::vector<float> values;
            int steps = 0, classes = 0;
            if (!Run(input.data(), width, values, steps, classes, &errors[k], cancel)) continue;
            lines[k] = DecodeCharacters(values.data(), steps, characters_, rows[k].rect);
            ok[k] = 1;
            millis[k] = std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now() - start).count();
        }
    };
    const size_t workers = std::max<size_t>(1, std::min<size_t>(static_cast<size_t>(std::max(1, threads)), rows.size()));
    std::vector<std::thread> helpers;
    for (size_t w = 1; w < workers; ++w) helpers.emplace_back(work, w, workers);
    work(0, workers);
    for (std::thread& h : helpers) h.join();
    out.clear();
    for (size_t k = 0; k < rows.size(); ++k) {
        if (!ok[k]) return fail(err, errors[k].empty() ? std::string("OCR row failed") : errors[k]);
        if (!lines[k].Words.empty()) out.push_back(std::move(lines[k]));
    }
    if (rowMillis != nullptr) *rowMillis = std::move(millis);
    return true;
}

Line DecodeCharacters(const float* values, int steps, const std::vector<std::string>& characters, const Rect& region) {
    struct Token {
        int index, step;
        float score;
    };
    std::vector<Token> tokens;
    const size_t n = characters.size();
    int previous = -1;
    for (int step = 0; step < steps; ++step) {
        const float* row = values + static_cast<size_t>(step) * n;
        int best = 0;
        for (size_t i = 0; i < n; ++i)
            if (row[i] > row[best]) best = static_cast<int>(i);
        if (best != 0 && best != previous) tokens.push_back(Token{best, step, row[best]});
        previous = best;
    }
    Line line;
    const double dx = region.Dx(), dy = region.Dy();
    for (size_t i = 0; i < tokens.size(); ++i) {
        const Token& t = tokens[i];
        std::string text = characters[static_cast<size_t>(t.index)];
        if (!text.empty() && text.back() == '\r') text.pop_back();
        if (t.score < 0.45f) text = "\xEF\xBF\xBD";  // U+FFFD
        const double center = (static_cast<double>(t.step) + .5) * dx / static_cast<double>(steps);
        double left = center - 8 * dy / 24, right = center + 8 * dy / 24;
        if (i > 0) left = (static_cast<double>(tokens[i - 1].step + t.step) + 1) / 2 * dx / static_cast<double>(steps);
        if (i + 1 < tokens.size()) right = (static_cast<double>(tokens[i + 1].step + t.step) + 1) / 2 * dx / static_cast<double>(steps);
        left = std::max(0.0, left);
        right = std::min(dx, right);
        Word w;
        w.Text = text;
        w.X = static_cast<double>(region.Min.X) + left;
        w.Y = static_cast<double>(region.Min.Y);
        w.Width = right - left;
        w.Height = dy;
        line.Words.push_back(std::move(w));
        line.Text += text;
    }
    return line;
}

}  // namespace nt::ocr
