// Owner: ocr（安卓本机文字识别，见 nt/ocr.h 与 nt/ocr_net.h）
// ONNX 模型解析（最小 protobuf 读取）与逐节点执行。
//
// 只实现 PP-OCRv4 识别模型（paddle2onnx 导出，opset 12）用到的算子，语义按 ONNX 规范：
//   Constant Conv BatchNormalization Mul Add Sub Div Pow Sqrt Clip Relu Sigmoid HardSigmoid
//   GlobalAveragePool AveragePool MatMul Softmax ReduceMean Transpose Reshape Squeeze Slice
//   Concat Shape Cast
// 不认识的算子、属性取值（膨胀卷积、非逐通道分组卷积、带填充的平均池化 …）一律报错。
#include <algorithm>
#include <cmath>
#include <cstring>
#include <limits>
#include <map>
#include <unordered_map>

#include "nt/ocr_net.h"

namespace nt::ocr {

// A run of consecutive element-wise nodes on one root tensor, executed as one pass over
// cache-sized chunks (Net::fuse). Every element sees exactly the operations of the unfused
// nodes, in the same order, so the result is identical to running them one by one.
enum class Code : uint8_t { MulC, AddC, SubC, RSubC, DivC, RDivC, Mul, Add, Sub, Div, Sqr, Sqrt, Clip, Relu, Sigmoid, HardSigmoid };
struct Instr {
    Code code;
    int dst = 0, a = 0, b = 0;
    float c = 0, c2 = 0;
};
struct Group {
    int root = -1;      // value read into register 0
    int last = -1;      // index of the group's last node
    int regs = 1;
    std::vector<Instr> code;
    std::vector<std::pair<int, int>> outputs;  // (register, value) materialised for later nodes
};

struct Node {
    std::string op;
    std::vector<int> in, out;
    std::map<std::string, Attr> attrs;
    TensorPtr value;  // Constant
    std::shared_ptr<Group> group;  // first node of a fused group
    bool fused = false;            // executed by an earlier node's group

    const Attr* attr(const char* name) const {
        auto it = attrs.find(name);
        return it == attrs.end() ? nullptr : &it->second;
    }
    int64_t attrInt(const char* name, int64_t def) const {
        const Attr* a = attr(name);
        return a != nullptr && a->hasI ? a->i : def;
    }
    float attrFloat(const char* name, float def) const {
        const Attr* a = attr(name);
        return a != nullptr && a->hasF ? a->f : def;
    }
    std::vector<int64_t> attrInts(const char* name, std::vector<int64_t> def) const {
        const Attr* a = attr(name);
        return a != nullptr && a->hasInts ? a->ints : def;
    }
};

namespace {

bool fail(std::string* err, const std::string& msg) {
    if (err != nullptr) *err = msg;
    return false;
}

// ---------------------------------------------------------------------------
// protobuf wire format
// ---------------------------------------------------------------------------
struct Wire {
    const uint8_t* p;
    const uint8_t* end;
    bool ok = true;

    bool done() const { return !ok || p >= end; }
    uint64_t varint() {
        uint64_t v = 0;
        for (int shift = 0; shift < 64 && p < end; shift += 7) {
            const uint8_t b = *p++;
            v |= static_cast<uint64_t>(b & 0x7f) << shift;
            if ((b & 0x80) == 0) return v;
        }
        ok = false;
        return 0;
    }
    bool key(uint32_t& field, uint32_t& wire) {
        const uint64_t k = varint();
        field = static_cast<uint32_t>(k >> 3);
        wire = static_cast<uint32_t>(k & 7);
        return ok;
    }
    bool bytes(const uint8_t*& b, size_t& n) {
        const uint64_t len = varint();
        if (!ok || len > static_cast<uint64_t>(end - p)) return ok = false;
        b = p;
        n = static_cast<size_t>(len);
        p += n;
        return true;
    }
    uint32_t fixed32() {
        if (end - p < 4) {
            ok = false;
            return 0;
        }
        uint32_t v;
        std::memcpy(&v, p, 4);
        p += 4;
        return v;
    }
    bool skip(uint32_t wire) {
        switch (wire) {
            case 0: varint(); return ok;
            case 1:
                if (end - p < 8) return ok = false;
                p += 8;
                return true;
            case 2: {
                const uint8_t* b;
                size_t n;
                return bytes(b, n);
            }
            case 5:
                if (end - p < 4) return ok = false;
                p += 4;
                return true;
            default: return ok = false;
        }
    }
    std::string str() {
        const uint8_t* b = nullptr;
        size_t n = 0;
        if (!bytes(b, n)) return {};
        return std::string(reinterpret_cast<const char*>(b), n);
    }
};

// Repeated scalar field: packed (wire 2) or one element (wire 0 / 5).
void varints(Wire& w, uint32_t wire, std::vector<int64_t>& out) {
    if (wire == 2) {
        const uint8_t* b;
        size_t n;
        if (!w.bytes(b, n)) return;
        Wire in{b, b + n};
        while (!in.done()) out.push_back(static_cast<int64_t>(in.varint()));
        if (!in.ok) w.ok = false;
    } else if (wire == 0) {
        out.push_back(static_cast<int64_t>(w.varint()));
    } else {
        w.ok = false;
    }
}

void floats(Wire& w, uint32_t wire, std::vector<float>& out) {
    if (wire == 2) {
        const uint8_t* b;
        size_t n;
        if (!w.bytes(b, n) || n % 4 != 0) {
            w.ok = false;
            return;
        }
        const size_t at = out.size();
        out.resize(at + n / 4);
        std::memcpy(out.data() + at, b, n);
    } else if (wire == 5) {
        const uint32_t v = w.fixed32();
        float f;
        std::memcpy(&f, &v, 4);
        out.push_back(f);
    } else {
        w.ok = false;
    }
}

// TensorProto: dims 1, data_type 2, float_data 4, int32_data 5, int64_data 7, raw_data 9.
bool parseTensor(const uint8_t* data, size_t size, Tensor& t, std::string* err) {
    Wire w{data, data + size};
    int64_t dtype = 0;
    const uint8_t* raw = nullptr;
    size_t rawSize = 0;
    std::vector<float> fdata;
    std::vector<int64_t> idata;
    while (!w.done()) {
        uint32_t field, wire;
        if (!w.key(field, wire)) break;
        switch (field) {
            case 1: varints(w, wire, t.shape); break;
            case 2: dtype = static_cast<int64_t>(w.varint()); break;
            case 4: floats(w, wire, fdata); break;
            case 5:
            case 7: varints(w, wire, idata); break;
            case 9:
                if (wire != 2 || !w.bytes(raw, rawSize)) w.ok = false;
                break;
            default: w.skip(wire);
        }
    }
    if (!w.ok) return fail(err, "bad TensorProto");
    for (int64_t d : t.shape)
        if (d < 0) return fail(err, "negative tensor dimension");
    const int64_t count = t.Size();
    switch (dtype) {
        case 1:  // FLOAT
            t.isFloat = true;
            if (raw != nullptr) {
                if (rawSize != static_cast<size_t>(count) * 4) return fail(err, "float tensor size mismatch");
                t.f.resize(static_cast<size_t>(count));
                if (count > 0) std::memcpy(t.f.data(), raw, rawSize);
            } else {
                if (fdata.size() != static_cast<size_t>(count)) return fail(err, "float tensor size mismatch");
                t.f = std::move(fdata);
            }
            return true;
        case 6:  // INT32
        case 7:  // INT64
            t.isFloat = false;
            if (raw != nullptr) {
                const size_t width = dtype == 6 ? 4 : 8;
                if (rawSize != static_cast<size_t>(count) * width) return fail(err, "int tensor size mismatch");
                t.i.resize(static_cast<size_t>(count));
                for (int64_t k = 0; k < count; ++k) {
                    if (dtype == 6) {
                        int32_t v;
                        std::memcpy(&v, raw + k * 4, 4);
                        t.i[static_cast<size_t>(k)] = v;
                    } else {
                        int64_t v;
                        std::memcpy(&v, raw + k * 8, 8);
                        t.i[static_cast<size_t>(k)] = v;
                    }
                }
            } else {
                if (idata.size() != static_cast<size_t>(count)) return fail(err, "int tensor size mismatch");
                t.i = std::move(idata);
                if (dtype == 6)
                    for (int64_t& v : t.i) v = static_cast<int32_t>(v);
            }
            return true;
        default: return fail(err, "unsupported tensor data type " + std::to_string(dtype));
    }
}

// AttributeProto: name 1, f 2, i 3, t 5, floats 7, ints 8.
bool parseAttr(const uint8_t* data, size_t size, std::string& name, Attr& a, TensorPtr& tensor, std::string* err) {
    Wire w{data, data + size};
    while (!w.done()) {
        uint32_t field, wire;
        if (!w.key(field, wire)) break;
        switch (field) {
            case 1: name = w.str(); break;
            case 2: {
                const uint32_t v = w.fixed32();
                std::memcpy(&a.f, &v, 4);
                a.hasF = true;
                break;
            }
            case 3:
                a.i = static_cast<int64_t>(w.varint());
                a.hasI = true;
                break;
            case 5: {
                const uint8_t* b;
                size_t n;
                if (!w.bytes(b, n)) break;
                tensor = std::make_shared<Tensor>();
                if (!parseTensor(b, n, *tensor, err)) return false;
                break;
            }
            case 8:
                varints(w, wire, a.ints);
                a.hasInts = true;
                break;
            default: w.skip(wire);
        }
    }
    return w.ok || fail(err, "bad AttributeProto");
}

// ---------------------------------------------------------------------------
// shapes and element-wise helpers
// ---------------------------------------------------------------------------
bool broadcastShape(const std::vector<int64_t>& a, const std::vector<int64_t>& b, std::vector<int64_t>& out) {
    const size_t rank = std::max(a.size(), b.size());
    out.assign(rank, 1);
    for (size_t k = 0; k < rank; ++k) {
        const int64_t da = k < rank - a.size() ? 1 : a[k - (rank - a.size())];
        const int64_t db = k < rank - b.size() ? 1 : b[k - (rank - b.size())];
        if (da != db && da != 1 && db != 1) return false;
        out[k] = da == 1 ? db : da;
    }
    return true;
}

// Element strides of t when broadcast to rank `rank` with output shape `shape` (0 = repeated).
std::vector<int64_t> broadcastStrides(const std::vector<int64_t>& t, const std::vector<int64_t>& shape) {
    const size_t rank = shape.size();
    std::vector<int64_t> s(rank, 0);
    int64_t stride = 1;
    for (size_t k = rank; k-- > 0;) {
        const size_t off = rank - t.size();
        if (k < off) continue;
        const int64_t d = t[k - off];
        s[k] = d == 1 ? 0 : stride;
        stride *= d;
    }
    return s;
}

template <class F>
bool binary(const Tensor& A, const Tensor& B, Tensor& Y, F f, std::string* err) {
    if (!A.isFloat || !B.isFloat) return fail(err, "integer arithmetic is not supported");
    if (!broadcastShape(A.shape, B.shape, Y.shape)) return fail(err, "shapes do not broadcast");
    const int64_t n = Y.Size();
    Y.isFloat = true;
    Y.f.resize(static_cast<size_t>(n));
    const float* a = A.f.data();
    const float* b = B.f.data();
    float* y = Y.f.data();
    if (A.shape == B.shape) {
        for (int64_t k = 0; k < n; ++k) y[k] = f(a[k], b[k]);
        return true;
    }
    if (B.Size() == 1 && A.Size() == n) {
        const float s = b[0];
        for (int64_t k = 0; k < n; ++k) y[k] = f(a[k], s);
        return true;
    }
    if (A.Size() == 1 && B.Size() == n) {
        const float s = a[0];
        for (int64_t k = 0; k < n; ++k) y[k] = f(s, b[k]);
        return true;
    }
    const size_t rank = Y.shape.size();
    const std::vector<int64_t> sa = broadcastStrides(A.shape, Y.shape), sb = broadcastStrides(B.shape, Y.shape);
    const int64_t inner = rank == 0 ? 1 : Y.shape[rank - 1];
    const int64_t ia = rank == 0 ? 0 : sa[rank - 1], ib = rank == 0 ? 0 : sb[rank - 1];
    std::vector<int64_t> idx(rank, 0);
    int64_t offA = 0, offB = 0;
    for (int64_t base = 0; base < n; base += inner) {
        for (int64_t x = 0; x < inner; ++x) y[base + x] = f(a[offA + x * ia], b[offB + x * ib]);
        // advance the outer odometer (all dims but the last)
        for (size_t k = rank - 1; k-- > 0;) {
            idx[k]++;
            offA += sa[k];
            offB += sb[k];
            if (idx[k] < Y.shape[k]) break;
            offA -= sa[k] * idx[k];
            offB -= sb[k] * idx[k];
            idx[k] = 0;
        }
    }
    return true;
}

template <class F>
bool unary(const Tensor& X, Tensor& Y, F f, std::string* err) {
    if (!X.isFloat) return fail(err, "integer math is not supported");
    Y.shape = X.shape;
    Y.isFloat = true;
    Y.f.resize(X.f.size());
    for (size_t k = 0; k < X.f.size(); ++k) Y.f[k] = f(X.f[k]);
    return true;
}

std::vector<int64_t> strides(const std::vector<int64_t>& shape) {
    std::vector<int64_t> s(shape.size(), 1);
    for (size_t k = shape.size(); k-- > 1;) s[k - 1] = s[k] * shape[k];
    return s;
}

int64_t normAxis(int64_t axis, size_t rank) { return axis < 0 ? axis + static_cast<int64_t>(rank) : axis; }

// ---------------------------------------------------------------------------
// operators
// ---------------------------------------------------------------------------

bool conv(const Node& node, const Tensor& X, const Tensor& W, const Tensor* B, Tensor& Y, std::string* err) {
    if (!X.isFloat || !W.isFloat || X.shape.size() != 4 || W.shape.size() != 4) return fail(err, "Conv: 2-D float only");
    const std::vector<int64_t> dil = node.attrInts("dilations", {1, 1});
    const std::vector<int64_t> st = node.attrInts("strides", {1, 1});
    const std::vector<int64_t> pads = node.attrInts("pads", {0, 0, 0, 0});
    const int64_t group = node.attrInt("group", 1);
    if (dil.size() != 2 || dil[0] != 1 || dil[1] != 1 || st.size() != 2 || pads.size() != 4 || st[0] < 1 || st[1] < 1)
        return fail(err, "Conv: unsupported attributes");
    const int64_t N = X.shape[0], C = X.shape[1], H = X.shape[2], Wd = X.shape[3];
    const int64_t M = W.shape[0], kh = W.shape[2], kw = W.shape[3];
    const int64_t pt = pads[0], pl = pads[1], pb = pads[2], pr = pads[3];
    if (group < 1 || C % group != 0 || W.shape[1] != C / group || M % group != 0) return fail(err, "Conv: bad group");
    if (B != nullptr && (!B->isFloat || B->Size() != M)) return fail(err, "Conv: bad bias");
    const int64_t OH = (H + pt + pb - kh) / st[0] + 1, OW = (Wd + pl + pr - kw) / st[1] + 1;
    if (OH < 1 || OW < 1) return fail(err, "Conv: empty output");
    Y.isFloat = true;
    Y.shape = {N, M, OH, OW};
    Y.f.assign(static_cast<size_t>(N * M * OH * OW), 0.0f);
    const float* bias = B != nullptr ? B->f.data() : nullptr;
    const bool depthwise = group == C && M == C && W.shape[1] == 1;
    if (!depthwise && group != 1) return fail(err, "Conv: only depthwise or ungrouped convolutions");
    for (int64_t n = 0; n < N; ++n) {
        const float* x = X.f.data() + n * C * H * Wd;
        float* y = Y.f.data() + n * M * OH * OW;
        if (depthwise) {
            DepthwiseConv(x, W.f.data(), bias, y, C, H, Wd, kh, kw, st[0], st[1], pt, pl, OH, OW);
            continue;
        }
        if (kh == 1 && kw == 1 && st[0] == 1 && st[1] == 1 && pt == 0 && pl == 0 && pb == 0 && pr == 0) {
            Gemm(static_cast<int>(M), static_cast<int>(OH * OW), static_cast<int>(C), W.f.data(), static_cast<int>(C), x,
                 static_cast<int>(H * Wd), y, static_cast<int>(OH * OW), bias);
            continue;
        }
        // im2col: rows (c, ky, kx), columns (oy, ox).
        const int64_t K = C * kh * kw, P = OH * OW;
        std::vector<float> col(static_cast<size_t>(K * P), 0.0f);
        for (int64_t c = 0; c < C; ++c)
            for (int64_t ky = 0; ky < kh; ++ky)
                for (int64_t kx = 0; kx < kw; ++kx) {
                    float* dst = col.data() + ((c * kh + ky) * kw + kx) * P;
                    for (int64_t oy = 0; oy < OH; ++oy) {
                        const int64_t iy = oy * st[0] - pt + ky;
                        if (iy < 0 || iy >= H) continue;
                        for (int64_t ox = 0; ox < OW; ++ox) {
                            const int64_t ix = ox * st[1] - pl + kx;
                            if (ix >= 0 && ix < Wd) dst[oy * OW + ox] = x[(c * H + iy) * Wd + ix];
                        }
                    }
                }
        Gemm(static_cast<int>(M), static_cast<int>(P), static_cast<int>(K), W.f.data(), static_cast<int>(K), col.data(),
             static_cast<int>(P), y, static_cast<int>(P), bias);
    }
    return true;
}

bool batchNorm(const Node& node, const std::vector<const Tensor*>& in, Tensor& Y, std::string* err) {
    const Tensor& X = *in[0];
    if (in.size() < 5 || !X.isFloat || X.shape.size() < 2) return fail(err, "BatchNormalization: bad inputs");
    const int64_t C = X.shape[1];
    for (int k = 1; k < 5; ++k)
        if (in[k] == nullptr || !in[k]->isFloat || in[k]->Size() != C) return fail(err, "BatchNormalization: bad parameters");
    const float eps = node.attrFloat("epsilon", 1e-5f);
    std::vector<float> a(static_cast<size_t>(C)), b(static_cast<size_t>(C));
    for (int64_t c = 0; c < C; ++c) {
        a[c] = in[1]->f[c] / std::sqrt(in[4]->f[c] + eps);
        b[c] = in[2]->f[c] - in[3]->f[c] * a[c];
    }
    int64_t inner = 1;
    for (size_t k = 2; k < X.shape.size(); ++k) inner *= X.shape[k];
    Y.shape = X.shape;
    Y.isFloat = true;
    Y.f.resize(X.f.size());
    const int64_t N = X.shape[0];
    for (int64_t n = 0; n < N; ++n)
        for (int64_t c = 0; c < C; ++c) {
            const float* x = X.f.data() + (n * C + c) * inner;
            float* y = Y.f.data() + (n * C + c) * inner;
            for (int64_t k = 0; k < inner; ++k) y[k] = x[k] * a[c] + b[c];
        }
    return true;
}

bool globalAvg(const Tensor& X, Tensor& Y, std::string* err) {
    if (!X.isFloat || X.shape.size() != 4) return fail(err, "GlobalAveragePool: 4-D float only");
    const int64_t N = X.shape[0], C = X.shape[1], HW = X.shape[2] * X.shape[3];
    Y.isFloat = true;
    Y.shape = {N, C, 1, 1};
    Y.f.resize(static_cast<size_t>(N * C));
    for (int64_t k = 0; k < N * C; ++k) {
        const float* x = X.f.data() + k * HW;
        float s = 0;
        for (int64_t j = 0; j < HW; ++j) s += x[j];
        Y.f[k] = s / static_cast<float>(HW);
    }
    return true;
}

bool avgPool(const Node& node, const Tensor& X, Tensor& Y, std::string* err) {
    const std::vector<int64_t> kernel = node.attrInts("kernel_shape", {});
    const std::vector<int64_t> st = node.attrInts("strides", {1, 1});
    const std::vector<int64_t> pads = node.attrInts("pads", {0, 0, 0, 0});
    if (!X.isFloat || X.shape.size() != 4 || kernel.size() != 2 || st.size() != 2 || node.attrInt("ceil_mode", 0) != 0 ||
        pads.size() != 4 || pads[0] != 0 || pads[1] != 0 || pads[2] != 0 || pads[3] != 0)
        return fail(err, "AveragePool: unsupported attributes");
    const int64_t N = X.shape[0], C = X.shape[1], H = X.shape[2], W = X.shape[3];
    const int64_t OH = (H - kernel[0]) / st[0] + 1, OW = (W - kernel[1]) / st[1] + 1;
    if (OH < 1 || OW < 1) return fail(err, "AveragePool: empty output");
    Y.isFloat = true;
    Y.shape = {N, C, OH, OW};
    Y.f.resize(static_cast<size_t>(N * C * OH * OW));
    const float count = static_cast<float>(kernel[0] * kernel[1]);
    for (int64_t nc = 0; nc < N * C; ++nc) {
        const float* x = X.f.data() + nc * H * W;
        float* y = Y.f.data() + nc * OH * OW;
        for (int64_t oy = 0; oy < OH; ++oy)
            for (int64_t ox = 0; ox < OW; ++ox) {
                float s = 0;
                for (int64_t ky = 0; ky < kernel[0]; ++ky)
                    for (int64_t kx = 0; kx < kernel[1]; ++kx) s += x[(oy * st[0] + ky) * W + ox * st[1] + kx];
                y[oy * OW + ox] = s / count;
            }
    }
    return true;
}

bool matMul(const Tensor& A, const Tensor& B, Tensor& Y, std::string* err) {
    if (!A.isFloat || !B.isFloat || A.shape.size() < 2 || B.shape.size() < 2) return fail(err, "MatMul: float matrices only");
    const int64_t M = A.shape[A.shape.size() - 2], K = A.shape.back();
    const int64_t N = B.shape.back();
    if (B.shape[B.shape.size() - 2] != K) return fail(err, "MatMul: inner dimensions differ");
    const std::vector<int64_t> ba(A.shape.begin(), A.shape.end() - 2), bb(B.shape.begin(), B.shape.end() - 2);
    std::vector<int64_t> batch;
    if (!broadcastShape(ba, bb, batch)) return fail(err, "MatMul: batch dimensions do not broadcast");
    Y.isFloat = true;
    Y.shape = batch;
    Y.shape.push_back(M);
    Y.shape.push_back(N);
    int64_t count = 1;
    for (int64_t d : batch) count *= d;
    Y.f.resize(static_cast<size_t>(count * M * N));
    const std::vector<int64_t> sa = broadcastStrides(ba, batch), sb = broadcastStrides(bb, batch);
    std::vector<int64_t> idx(batch.size(), 0);
    for (int64_t b = 0; b < count; ++b) {
        int64_t oa = 0, ob = 0;
        for (size_t k = 0; k < batch.size(); ++k) {
            oa += idx[k] * sa[k];
            ob += idx[k] * sb[k];
        }
        Gemm(static_cast<int>(M), static_cast<int>(N), static_cast<int>(K), A.f.data() + oa * M * K, static_cast<int>(K),
             B.f.data() + ob * K * N, static_cast<int>(N), Y.f.data() + b * M * N, static_cast<int>(N), nullptr);
        for (size_t k = batch.size(); k-- > 0;) {
            if (++idx[k] < batch[k]) break;
            idx[k] = 0;
        }
    }
    return true;
}

// opset < 13: the input is coerced to 2-D at `axis`; the softmax runs over the trailing block.
bool softmax(const Node& node, int opset, const Tensor& X, Tensor& Y, std::string* err) {
    if (!X.isFloat || X.shape.empty()) return fail(err, "Softmax: float only");
    const int64_t axis = normAxis(node.attrInt("axis", opset < 13 ? 1 : -1), X.shape.size());
    if (axis < 0 || axis >= static_cast<int64_t>(X.shape.size())) return fail(err, "Softmax: bad axis");
    int64_t inner = 1;
    if (opset < 13) {
        for (size_t k = static_cast<size_t>(axis); k < X.shape.size(); ++k) inner *= X.shape[k];
    } else {
        if (axis != static_cast<int64_t>(X.shape.size()) - 1) return fail(err, "Softmax: last axis only");
        inner = X.shape.back();
    }
    Y.shape = X.shape;
    Y.isFloat = true;
    Y.f.resize(X.f.size());
    const int64_t rows = inner == 0 ? 0 : X.Size() / inner;
    for (int64_t r = 0; r < rows; ++r) {
        const float* x = X.f.data() + r * inner;
        float* y = Y.f.data() + r * inner;
        float mx = -std::numeric_limits<float>::infinity();
        for (int64_t k = 0; k < inner; ++k) mx = std::max(mx, x[k]);
        float sum = 0;
        for (int64_t k = 0; k < inner; ++k) {
            y[k] = std::exp(x[k] - mx);
            sum += y[k];
        }
        for (int64_t k = 0; k < inner; ++k) y[k] /= sum;
    }
    return true;
}

bool reduceMean(const Node& node, const Tensor& X, Tensor& Y, std::string* err) {
    const std::vector<int64_t> axes = node.attrInts("axes", {});
    if (!X.isFloat || axes.size() != 1) return fail(err, "ReduceMean: one axis only");
    const int64_t axis = normAxis(axes[0], X.shape.size());
    if (axis < 0 || axis >= static_cast<int64_t>(X.shape.size())) return fail(err, "ReduceMean: bad axis");
    int64_t outer = 1, inner = 1;
    for (int64_t k = 0; k < axis; ++k) outer *= X.shape[k];
    for (size_t k = static_cast<size_t>(axis) + 1; k < X.shape.size(); ++k) inner *= X.shape[k];
    const int64_t d = X.shape[axis];
    Y.isFloat = true;
    Y.shape = X.shape;
    Y.shape[axis] = 1;
    if (node.attrInt("keepdims", 1) == 0) Y.shape.erase(Y.shape.begin() + axis);
    Y.f.assign(static_cast<size_t>(outer * inner), 0.0f);
    for (int64_t o = 0; o < outer; ++o)
        for (int64_t i = 0; i < inner; ++i) {
            float s = 0;
            for (int64_t k = 0; k < d; ++k) s += X.f[(o * d + k) * inner + i];
            Y.f[o * inner + i] = s / static_cast<float>(d);
        }
    return true;
}

bool transpose(const Node& node, const Tensor& X, Tensor& Y, std::string* err) {
    const size_t rank = X.shape.size();
    std::vector<int64_t> perm = node.attrInts("perm", {});
    if (perm.empty())
        for (size_t k = rank; k-- > 0;) perm.push_back(static_cast<int64_t>(k));
    if (perm.size() != rank) return fail(err, "Transpose: bad perm");
    Y.isFloat = X.isFloat;
    Y.shape.resize(rank);
    for (size_t k = 0; k < rank; ++k) {
        if (perm[k] < 0 || perm[k] >= static_cast<int64_t>(rank)) return fail(err, "Transpose: bad perm");
        Y.shape[k] = X.shape[perm[k]];
    }
    const std::vector<int64_t> sx = strides(X.shape);
    std::vector<int64_t> src(rank);
    for (size_t k = 0; k < rank; ++k) src[k] = sx[perm[k]];
    const int64_t n = X.Size();
    if (X.isFloat) Y.f.resize(static_cast<size_t>(n));
    else Y.i.resize(static_cast<size_t>(n));
    std::vector<int64_t> idx(rank, 0);
    int64_t off = 0;
    for (int64_t k = 0; k < n; ++k) {
        if (X.isFloat) Y.f[k] = X.f[off];
        else Y.i[k] = X.i[off];
        for (size_t d = rank; d-- > 0;) {
            idx[d]++;
            off += src[d];
            if (idx[d] < Y.shape[d]) break;
            off -= src[d] * idx[d];
            idx[d] = 0;
        }
    }
    return true;
}

bool reshape(const Tensor& X, const Tensor& S, Tensor& Y, std::string* err) {
    if (S.isFloat) return fail(err, "Reshape: shape must be integer");
    std::vector<int64_t> shape = S.i;
    int64_t known = 1;
    int infer = -1;
    for (size_t k = 0; k < shape.size(); ++k) {
        if (shape[k] == 0) {
            if (k >= X.shape.size()) return fail(err, "Reshape: 0 beyond input rank");
            shape[k] = X.shape[k];
        }
        if (shape[k] == -1) {
            if (infer >= 0) return fail(err, "Reshape: two -1");
            infer = static_cast<int>(k);
        } else if (shape[k] < 0) {
            return fail(err, "Reshape: negative dimension");
        } else {
            known *= shape[k];
        }
    }
    const int64_t n = X.Size();
    if (infer >= 0) {
        if (known == 0 || n % known != 0) return fail(err, "Reshape: cannot infer");
        shape[infer] = n / known;
    } else if (known != n) {
        return fail(err, "Reshape: size mismatch");
    }
    Y = X;
    Y.shape = shape;
    return true;
}

bool squeeze(const Node& node, const Tensor& X, Tensor& Y, std::string* err) {
    std::vector<int64_t> axes = node.attrInts("axes", {});
    std::vector<bool> drop(X.shape.size(), false);
    if (axes.empty()) {
        for (size_t k = 0; k < X.shape.size(); ++k) drop[k] = X.shape[k] == 1;
    } else {
        for (int64_t a : axes) {
            a = normAxis(a, X.shape.size());
            if (a < 0 || a >= static_cast<int64_t>(X.shape.size()) || X.shape[a] != 1) return fail(err, "Squeeze: bad axis");
            drop[a] = true;
        }
    }
    Y = X;
    Y.shape.clear();
    for (size_t k = 0; k < X.shape.size(); ++k)
        if (!drop[k]) Y.shape.push_back(X.shape[k]);
    return true;
}

bool slice(const std::vector<const Tensor*>& in, Tensor& Y, std::string* err) {
    const Tensor& X = *in[0];
    if (in.size() < 3 || in[1] == nullptr || in[2] == nullptr || in[1]->isFloat || in[2]->isFloat)
        return fail(err, "Slice: integer starts/ends required");
    const size_t rank = X.shape.size();
    const std::vector<int64_t>& starts = in[1]->i;
    const std::vector<int64_t>& ends = in[2]->i;
    std::vector<int64_t> axes, steps;
    if (in.size() > 3 && in[3] != nullptr) axes = in[3]->i;
    else
        for (size_t k = 0; k < starts.size(); ++k) axes.push_back(static_cast<int64_t>(k));
    if (in.size() > 4 && in[4] != nullptr) steps = in[4]->i;
    else steps.assign(starts.size(), 1);
    if (ends.size() != starts.size() || axes.size() != starts.size() || steps.size() != starts.size())
        return fail(err, "Slice: argument lengths differ");
    std::vector<int64_t> begin(rank, 0), count = X.shape;
    for (size_t k = 0; k < starts.size(); ++k) {
        const int64_t a = normAxis(axes[k], rank);
        if (a < 0 || a >= static_cast<int64_t>(rank) || steps[k] != 1) return fail(err, "Slice: unsupported axis/step");
        const int64_t d = X.shape[a];
        int64_t s = starts[k], e = ends[k];
        if (s < 0) s += d;
        if (e < 0) e += d;
        s = std::clamp<int64_t>(s, 0, d);
        e = std::clamp<int64_t>(e, 0, d);
        begin[a] = s;
        count[a] = std::max<int64_t>(0, e - s);
    }
    Y.isFloat = X.isFloat;
    Y.shape = count;
    const int64_t n = Y.Size();
    if (X.isFloat) Y.f.resize(static_cast<size_t>(n));
    else Y.i.resize(static_cast<size_t>(n));
    const std::vector<int64_t> sx = strides(X.shape);
    std::vector<int64_t> idx(rank, 0);
    for (int64_t k = 0; k < n; ++k) {
        int64_t off = 0;
        for (size_t d = 0; d < rank; ++d) off += (begin[d] + idx[d]) * sx[d];
        if (X.isFloat) Y.f[k] = X.f[off];
        else Y.i[k] = X.i[off];
        for (size_t d = rank; d-- > 0;) {
            if (++idx[d] < count[d]) break;
            idx[d] = 0;
        }
    }
    return true;
}

bool concat(const Node& node, const std::vector<const Tensor*>& in, Tensor& Y, std::string* err) {
    if (in.empty() || in[0] == nullptr) return fail(err, "Concat: no inputs");
    const Tensor& first = *in[0];
    const size_t rank = first.shape.size();
    const int64_t axis = normAxis(node.attrInt("axis", 0), rank);
    if (axis < 0 || axis >= static_cast<int64_t>(rank)) return fail(err, "Concat: bad axis");
    Y.isFloat = first.isFloat;
    Y.shape = first.shape;
    Y.shape[axis] = 0;
    for (const Tensor* t : in) {
        if (t == nullptr || t->isFloat != first.isFloat || t->shape.size() != rank) return fail(err, "Concat: mixed inputs");
        for (size_t k = 0; k < rank; ++k)
            if (static_cast<int64_t>(k) != axis && t->shape[k] != first.shape[k]) return fail(err, "Concat: shapes differ");
        Y.shape[axis] += t->shape[axis];
    }
    int64_t outer = 1, inner = 1;
    for (int64_t k = 0; k < axis; ++k) outer *= Y.shape[k];
    for (size_t k = static_cast<size_t>(axis) + 1; k < rank; ++k) inner *= Y.shape[k];
    if (Y.isFloat) Y.f.resize(static_cast<size_t>(Y.Size()));
    else Y.i.resize(static_cast<size_t>(Y.Size()));
    int64_t at = 0;
    for (const Tensor* t : in) {
        const int64_t block = t->shape[axis] * inner;
        for (int64_t o = 0; o < outer; ++o) {
            const int64_t dst = o * Y.shape[axis] * inner + at * inner;
            if (Y.isFloat) std::copy_n(t->f.data() + o * block, block, Y.f.data() + dst);
            else std::copy_n(t->i.data() + o * block, block, Y.i.data() + dst);
        }
        at += t->shape[axis];
    }
    return true;
}

bool cast(const Node& node, const Tensor& X, Tensor& Y, std::string* err) {
    const int64_t to = node.attrInt("to", 0);
    Y.shape = X.shape;
    if (to == 1) {
        Y.isFloat = true;
        if (X.isFloat) Y.f = X.f;
        else Y.f.assign(X.i.begin(), X.i.end());
        return true;
    }
    if (to == 6 || to == 7) {
        Y.isFloat = false;
        if (X.isFloat) {
            Y.i.resize(X.f.size());
            for (size_t k = 0; k < X.f.size(); ++k) Y.i[k] = static_cast<int64_t>(X.f[k]);
        } else {
            Y.i = X.i;
        }
        if (to == 6)
            for (int64_t& v : Y.i) v = static_cast<int32_t>(v);
        return true;
    }
    return fail(err, "Cast: unsupported target type " + std::to_string(to));
}

bool scalarConst(const std::vector<TensorPtr>& constants, int v, float& out) {
    if (v < 0 || constants[static_cast<size_t>(v)] == nullptr) return false;
    const Tensor& t = *constants[static_cast<size_t>(v)];
    if (!t.isFloat || t.Size() != 1) return false;
    out = t.f[0];
    return true;
}

// Groups consecutive element-wise nodes whose non-constant inputs are one root value or earlier
// results of the same group; constants must be scalars. Values read outside the group (or the
// graph output) are written out.
void fuseElementwise(std::vector<std::unique_ptr<Node>>& nodes, const std::vector<TensorPtr>& constants, int output,
                     int opset) {
    std::vector<std::vector<int>> consumers(constants.size());
    for (size_t n = 0; n < nodes.size(); ++n)
        for (int v : nodes[n]->in)
            if (v >= 0) consumers[static_cast<size_t>(v)].push_back(static_cast<int>(n));

    std::shared_ptr<Group> g;
    int first = -1;
    std::map<int, int> reg;  // value -> register
    std::vector<int> members;
    auto close = [&]() {
        if (g != nullptr && members.size() >= 2) {
            for (int n : members) {
                const int v = nodes[static_cast<size_t>(n)]->out[0];
                bool outside = v == output;
                for (int c : consumers[static_cast<size_t>(v)])
                    if (std::find(members.begin(), members.end(), c) == members.end()) outside = true;
                if (outside) g->outputs.emplace_back(reg[v], v);
            }
            g->last = members.back();
            nodes[static_cast<size_t>(first)]->group = g;
            for (size_t k = 1; k < members.size(); ++k) nodes[static_cast<size_t>(members[k])]->fused = true;
        }
        g.reset();
        reg.clear();
        members.clear();
        first = -1;
    };
    // Instruction for node n with the current registers; false = not fusable here.
    auto compile = [&](const Node& node, Instr& ins) {
        const std::string& op = node.op;
        auto regOf = [&](int v, int& r) {
            auto it = reg.find(v);
            if (it == reg.end()) return false;
            r = it->second;
            return true;
        };
        if (node.in.empty() || node.out.size() != 1) return false;
        if (op == "Mul" || op == "Add" || op == "Sub" || op == "Div") {
            if (node.in.size() != 2) return false;
            int ra, rb;
            float c;
            const bool aReg = regOf(node.in[0], ra), bReg = regOf(node.in[1], rb);
            if (aReg && bReg) {
                ins.code = op == "Mul" ? Code::Mul : op == "Add" ? Code::Add : op == "Sub" ? Code::Sub : Code::Div;
                ins.a = ra;
                ins.b = rb;
                return true;
            }
            if (aReg && scalarConst(constants, node.in[1], c)) {
                ins.code = op == "Mul" ? Code::MulC : op == "Add" ? Code::AddC : op == "Sub" ? Code::SubC : Code::DivC;
                ins.a = ra;
                ins.c = c;
                return true;
            }
            if (bReg && scalarConst(constants, node.in[0], c)) {
                // c·x and c+x are written as x·c and x+c: IEEE multiplication and addition commute.
                ins.code = op == "Mul" ? Code::MulC : op == "Add" ? Code::AddC : op == "Sub" ? Code::RSubC : Code::RDivC;
                ins.a = rb;
                ins.c = c;
                return true;
            }
            return false;
        }
        int ra;
        if (!regOf(node.in[0], ra)) return false;
        ins.a = ra;
        if (op == "Pow") {
            float c;
            if (node.in.size() != 2 || !scalarConst(constants, node.in[1], c) || c != 2.0f) return false;
            ins.code = Code::Sqr;
            return true;
        }
        if (op == "Clip") {
            float lo = -std::numeric_limits<float>::infinity(), hi = std::numeric_limits<float>::infinity();
            if (opset < 11) {
                lo = node.attrFloat("min", lo);
                hi = node.attrFloat("max", hi);
            } else {
                if (node.in.size() > 1 && node.in[1] >= 0 && !scalarConst(constants, node.in[1], lo)) return false;
                if (node.in.size() > 2 && node.in[2] >= 0 && !scalarConst(constants, node.in[2], hi)) return false;
            }
            ins.code = Code::Clip;
            ins.c = lo;
            ins.c2 = hi;
            return true;
        }
        if (node.in.size() != 1) return false;
        if (op == "Sqrt") ins.code = Code::Sqrt;
        else if (op == "Relu") ins.code = Code::Relu;
        else if (op == "Sigmoid") ins.code = Code::Sigmoid;
        else if (op == "HardSigmoid") {
            ins.code = Code::HardSigmoid;
            ins.c = node.attrFloat("alpha", 0.2f);
            ins.c2 = node.attrFloat("beta", 0.5f);
        } else {
            return false;
        }
        return true;
    };
    for (size_t n = 0; n < nodes.size(); ++n) {
        Node& node = *nodes[n];
        if (node.op == "Constant") continue;
        Instr ins;
        if (g != nullptr && compile(node, ins)) {
            ins.dst = g->regs++;
            reg[node.out[0]] = ins.dst;
            g->code.push_back(ins);
            members.push_back(static_cast<int>(n));
            continue;
        }
        close();
        // Start a group at an element-wise node with exactly one non-constant (root) input.
        int root = -1;
        bool single = true;
        for (int v : node.in) {
            if (v < 0 || constants[static_cast<size_t>(v)] != nullptr) continue;
            if (root >= 0 && root != v) single = false;
            root = v;
        }
        if (root < 0 || !single) continue;
        g = std::make_shared<Group>();
        g->root = root;
        reg[root] = 0;
        if (!compile(node, ins)) {
            g.reset();
            reg.clear();
            continue;
        }
        ins.dst = g->regs++;
        reg[node.out[0]] = ins.dst;
        g->code.push_back(ins);
        first = static_cast<int>(n);
        members.push_back(first);
    }
    close();
}

constexpr int kChunk = 512;

// PP-LCNetV3's learnable affine + hardswish + affine after every backbone convolution:
//   r1 = r0·c  r2 = r1+c  r3 = r2+c  r4 = clip(r3)  r5 = r2·r4  r6 = r5/c  r7 = r6·c  r8 = r7+c
// (most of the element-wise work). One loop, the same operations in the same order.
bool affineHardswish(const Group& g) {
    static const Code want[] = {Code::MulC, Code::AddC, Code::AddC, Code::Clip, Code::Mul, Code::DivC, Code::MulC, Code::AddC};
    if (g.code.size() != 8 || g.outputs.size() != 1 || g.outputs[0].first != 8) return false;
    for (int k = 0; k < 8; ++k) {
        const Instr& in = g.code[static_cast<size_t>(k)];
        if (in.code != want[k] || in.dst != k + 1) return false;
    }
    const auto& c = g.code;
    return c[0].a == 0 && c[1].a == 1 && c[2].a == 2 && c[3].a == 3 &&
           ((c[4].a == 2 && c[4].b == 4) || (c[4].a == 4 && c[4].b == 2)) && c[5].a == 5 && c[6].a == 6 && c[7].a == 7;
}

// Runs a fused group over x; writes the group's outputs.
void runGroup(const Group& g, const Tensor& x, std::vector<TensorPtr>& outs) {
    const size_t n = x.f.size();
    outs.clear();
    for (size_t k = 0; k < g.outputs.size(); ++k) {
        auto t = std::make_shared<Tensor>();
        t->shape = x.shape;
        t->isFloat = true;
        t->f.resize(n);
        outs.push_back(std::move(t));
    }
    if (affineHardswish(g)) {
        const auto& c = g.code;
        const float m0 = c[0].c, a1 = c[1].c, a2 = c[2].c, lo = c[3].c, hi = c[3].c2, d5 = c[5].c, m6 = c[6].c, a7 = c[7].c;
        const float* __restrict src = x.f.data();
        float* __restrict dst = outs[0]->f.data();
        for (size_t k = 0; k < n; ++k) {
            const float r1 = src[k] * m0;
            const float r2 = r1 + a1;
            const float r3 = r2 + a2;
            const float r4 = std::min(hi, std::max(lo, r3));
            const float r5 = r2 * r4;
            const float r6 = r5 / d5;
            const float r7 = r6 * m6;
            dst[k] = r7 + a7;
        }
        return;
    }
    std::vector<float> regs(static_cast<size_t>(g.regs) * kChunk);
    for (size_t base = 0; base < n; base += kChunk) {
        const int len = static_cast<int>(std::min<size_t>(kChunk, n - base));
        std::memcpy(regs.data(), x.f.data() + base, sizeof(float) * len);
        for (const Instr& in : g.code) {
            float* __restrict d = regs.data() + static_cast<size_t>(in.dst) * kChunk;
            const float* a = regs.data() + static_cast<size_t>(in.a) * kChunk;
            const float* b = regs.data() + static_cast<size_t>(in.b) * kChunk;
            const float c = in.c, c2 = in.c2;
            switch (in.code) {
                case Code::MulC: for (int k = 0; k < len; ++k) d[k] = a[k] * c; break;
                case Code::AddC: for (int k = 0; k < len; ++k) d[k] = a[k] + c; break;
                case Code::SubC: for (int k = 0; k < len; ++k) d[k] = a[k] - c; break;
                case Code::RSubC: for (int k = 0; k < len; ++k) d[k] = c - a[k]; break;
                case Code::DivC: for (int k = 0; k < len; ++k) d[k] = a[k] / c; break;
                case Code::RDivC: for (int k = 0; k < len; ++k) d[k] = c / a[k]; break;
                case Code::Mul: for (int k = 0; k < len; ++k) d[k] = a[k] * b[k]; break;
                case Code::Add: for (int k = 0; k < len; ++k) d[k] = a[k] + b[k]; break;
                case Code::Sub: for (int k = 0; k < len; ++k) d[k] = a[k] - b[k]; break;
                case Code::Div: for (int k = 0; k < len; ++k) d[k] = a[k] / b[k]; break;
                case Code::Sqr: for (int k = 0; k < len; ++k) d[k] = a[k] * a[k]; break;
                case Code::Sqrt: for (int k = 0; k < len; ++k) d[k] = std::sqrt(a[k]); break;
                case Code::Clip: for (int k = 0; k < len; ++k) d[k] = std::min(c2, std::max(c, a[k])); break;
                case Code::Relu: for (int k = 0; k < len; ++k) d[k] = a[k] > 0 ? a[k] : 0.0f; break;
                case Code::Sigmoid: for (int k = 0; k < len; ++k) d[k] = 1.0f / (1.0f + std::exp(-a[k])); break;
                case Code::HardSigmoid:
                    for (int k = 0; k < len; ++k) d[k] = std::max(0.0f, std::min(1.0f, c * a[k] + c2));
                    break;
            }
        }
        for (size_t k = 0; k < g.outputs.size(); ++k)
            std::memcpy(outs[k]->f.data() + base, regs.data() + static_cast<size_t>(g.outputs[k].first) * kChunk,
                        sizeof(float) * len);
    }
}

}  // namespace

Net::Net() = default;
Net::~Net() = default;

bool Net::Parse(const uint8_t* data, size_t size, std::string* err) {
    Wire model{data, data + size};
    const uint8_t* graph = nullptr;
    size_t graphSize = 0;
    int opset = 0;
    while (!model.done()) {
        uint32_t field, wire;
        if (!model.key(field, wire)) break;
        if (field == 7 && wire == 2) {
            if (!model.bytes(graph, graphSize)) break;
        } else if (field == 8 && wire == 2) {  // OperatorSetIdProto: domain 1, version 2
            const uint8_t* b;
            size_t n;
            if (!model.bytes(b, n)) break;
            Wire o{b, b + n};
            std::string domain;
            int64_t version = 0;
            while (!o.done()) {
                uint32_t f2, w2;
                if (!o.key(f2, w2)) break;
                if (f2 == 1) domain = o.str();
                else if (f2 == 2) version = static_cast<int64_t>(o.varint());
                else o.skip(w2);
            }
            if (domain.empty() || domain == "ai.onnx") opset = static_cast<int>(version);
        } else if (field == 14 && wire == 2) {  // StringStringEntryProto: key 1, value 2
            const uint8_t* b;
            size_t n;
            if (!model.bytes(b, n)) break;
            Wire e{b, b + n};
            std::string key, val;
            while (!e.done()) {
                uint32_t f2, w2;
                if (!e.key(f2, w2)) break;
                if (f2 == 1) key = e.str();
                else if (f2 == 2) val = e.str();
                else e.skip(w2);
            }
            if (key == "character") dictionary = val;
        } else {
            model.skip(wire);
        }
    }
    if (!model.ok || graph == nullptr) return fail(err, "bad ModelProto");
    if (opset < 7 || opset > 13) return fail(err, "unsupported opset " + std::to_string(opset));
    opset_ = opset;

    std::unordered_map<std::string, int> index;
    auto valueOf = [&](const std::string& name) {
        auto it = index.find(name);
        if (it != index.end()) return it->second;
        const int v = static_cast<int>(names_.size());
        names_.push_back(name);
        index.emplace(name, v);
        return v;
    };
    std::vector<std::pair<int, TensorPtr>> consts;
    std::string inputName, outputName;
    Wire g{graph, graph + graphSize};
    while (!g.done()) {
        uint32_t field, wire;
        if (!g.key(field, wire)) break;
        if (wire != 2) {
            g.skip(wire);
            continue;
        }
        const uint8_t* b;
        size_t n;
        if (!g.bytes(b, n)) break;
        Wire w{b, b + n};
        if (field == 1) {  // NodeProto: input 1, output 2, op_type 4, attribute 5, domain 7
            auto node = std::make_unique<Node>();
            std::vector<std::string> ins, outs;
            std::string domain;
            while (!w.done()) {
                uint32_t f2, w2;
                if (!w.key(f2, w2)) break;
                switch (f2) {
                    case 1: ins.push_back(w.str()); break;
                    case 2: outs.push_back(w.str()); break;
                    case 4: node->op = w.str(); break;
                    case 7: domain = w.str(); break;
                    case 5: {
                        const uint8_t* ab;
                        size_t an;
                        if (!w.bytes(ab, an)) break;
                        std::string name;
                        Attr a;
                        TensorPtr t;
                        if (!parseAttr(ab, an, name, a, t, err)) return false;
                        if (t != nullptr) node->value = t;
                        node->attrs[name] = std::move(a);
                        break;
                    }
                    default: w.skip(w2);
                }
            }
            if (!w.ok) return fail(err, "bad NodeProto");
            if (!domain.empty() && domain != "ai.onnx") return fail(err, "unsupported operator domain " + domain);
            for (const std::string& s : ins) node->in.push_back(s.empty() ? -1 : valueOf(s));
            for (const std::string& s : outs) node->out.push_back(valueOf(s));
            if (node->out.empty()) return fail(err, node->op + ": no output");
            nodes_.push_back(std::move(node));
        } else if (field == 5) {  // initializer
            auto t = std::make_shared<Tensor>();
            std::string name;
            // TensorProto name is field 8.
            Wire nw{b, b + n};
            while (!nw.done()) {
                uint32_t f2, w2;
                if (!nw.key(f2, w2)) break;
                if (f2 == 8) name = nw.str();
                else nw.skip(w2);
            }
            if (!parseTensor(b, n, *t, err)) return false;
            consts.emplace_back(valueOf(name), t);
        } else if (field == 11 || field == 12) {  // ValueInfoProto: name 1
            std::string name;
            while (!w.done()) {
                uint32_t f2, w2;
                if (!w.key(f2, w2)) break;
                if (f2 == 1) name = w.str();
                else w.skip(w2);
            }
            if (field == 11 && inputName.empty()) inputName = name;
            if (field == 12) outputName = name;
        }
    }
    if (!g.ok) return fail(err, "bad GraphProto");
    if (inputName.empty() || outputName.empty()) return fail(err, "graph input/output missing");
    static const char* const kOps[] = {"Constant", "Conv", "BatchNormalization", "Mul", "Add", "Sub", "Div", "Pow",
                                       "Sqrt", "Clip", "Relu", "Sigmoid", "HardSigmoid", "GlobalAveragePool",
                                       "AveragePool", "MatMul", "Softmax", "ReduceMean", "Transpose", "Reshape",
                                       "Squeeze", "Slice", "Concat", "Shape", "Cast"};
    for (const auto& node : nodes_) {
        if (std::find_if(std::begin(kOps), std::end(kOps), [&](const char* s) { return node->op == s; }) == std::end(kOps))
            return fail(err, "unsupported operator " + node->op);
        if (node->op == "Constant") {
            if (node->value == nullptr) return fail(err, "Constant without tensor value");
            consts.emplace_back(node->out[0], node->value);
            node->value.reset();
        }
    }
    input_ = valueOf(inputName);
    output_ = valueOf(outputName);
    constants_.assign(names_.size(), nullptr);
    weightBytes_ = 0;
    for (auto& [v, t] : consts) {
        weightBytes_ += t->f.size() * sizeof(float) + t->i.size() * sizeof(int64_t);
        constants_[static_cast<size_t>(v)] = std::move(t);
    }
    lastUse_.assign(names_.size(), -1);
    for (size_t n = 0; n < nodes_.size(); ++n)
        for (int v : nodes_[n]->in)
            if (v >= 0) lastUse_[static_cast<size_t>(v)] = static_cast<int>(n);
    fuseElementwise(nodes_, constants_, output_, opset_);
    return true;
}

bool Net::Run(const float* input, int64_t width, std::vector<float>& out, std::vector<int64_t>& shape,
              std::string* err, const std::atomic<bool>* cancel) const {
    if (input_ < 0 || output_ < 0) return fail(err, "model not loaded");
    std::vector<TensorPtr> vals(names_.size());
    for (size_t v = 0; v < constants_.size(); ++v) vals[v] = constants_[v];
    {
        auto x = std::make_shared<Tensor>();
        x->shape = {1, 3, 48, width};
        x->f.assign(input, input + 3 * 48 * width);
        vals[static_cast<size_t>(input_)] = std::move(x);
    }
    std::vector<const Tensor*> in;
    for (size_t n = 0; n < nodes_.size(); ++n) {
        const Node& node = *nodes_[n];
        if (node.op == "Constant" || node.fused) continue;
        if (cancel != nullptr && cancel->load(std::memory_order_relaxed)) return fail(err, "cancelled");
        if (node.group != nullptr) {
            const Group& g = *node.group;
            const Tensor* x = vals[static_cast<size_t>(g.root)].get();
            if (x == nullptr || !x->isFloat) return fail(err, node.op + ": missing float input");
            std::vector<TensorPtr> outs;
            runGroup(g, *x, outs);
            for (size_t k = 0; k < outs.size(); ++k) vals[static_cast<size_t>(g.outputs[k].second)] = std::move(outs[k]);
            if (constants_[static_cast<size_t>(g.root)] == nullptr && g.root != output_ &&
                lastUse_[static_cast<size_t>(g.root)] <= g.last)
                vals[static_cast<size_t>(g.root)].reset();
            continue;
        }
        in.clear();
        for (int v : node.in) {
            const Tensor* t = v >= 0 ? vals[static_cast<size_t>(v)].get() : nullptr;
            in.push_back(t);
        }
        if (in.empty() || in[0] == nullptr) return fail(err, node.op + ": missing input");
        auto y = std::make_shared<Tensor>();
        bool ok = true;
        const std::string& op = node.op;
        auto need = [&](size_t k) { return in.size() > k && in[k] != nullptr; };
        if (op == "Conv") {
            ok = need(1) && conv(node, *in[0], *in[1], need(2) ? in[2] : nullptr, *y, err);
        } else if (op == "BatchNormalization") {
            ok = batchNorm(node, in, *y, err);
        } else if (op == "Mul") {
            ok = need(1) && binary(*in[0], *in[1], *y, [](float a, float b) { return a * b; }, err);
        } else if (op == "Add") {
            ok = need(1) && binary(*in[0], *in[1], *y, [](float a, float b) { return a + b; }, err);
        } else if (op == "Sub") {
            ok = need(1) && binary(*in[0], *in[1], *y, [](float a, float b) { return a - b; }, err);
        } else if (op == "Div") {
            ok = need(1) && binary(*in[0], *in[1], *y, [](float a, float b) { return a / b; }, err);
        } else if (op == "Pow") {
            if (need(1) && in[1]->isFloat && in[1]->Size() == 1 && in[1]->f[0] == 2.0f) {
                ok = unary(*in[0], *y, [](float a) { return a * a; }, err);
            } else {
                ok = need(1) && binary(*in[0], *in[1], *y, [](float a, float b) { return std::pow(a, b); }, err);
            }
        } else if (op == "Sqrt") {
            ok = unary(*in[0], *y, [](float a) { return std::sqrt(a); }, err);
        } else if (op == "Relu") {
            ok = unary(*in[0], *y, [](float a) { return a > 0 ? a : 0.0f; }, err);
        } else if (op == "Sigmoid") {
            ok = unary(*in[0], *y, [](float a) { return 1.0f / (1.0f + std::exp(-a)); }, err);
        } else if (op == "HardSigmoid") {
            const float alpha = node.attrFloat("alpha", 0.2f), beta = node.attrFloat("beta", 0.5f);
            ok = unary(*in[0], *y, [=](float a) { return std::max(0.0f, std::min(1.0f, alpha * a + beta)); }, err);
        } else if (op == "Clip") {
            float lo = -std::numeric_limits<float>::infinity(), hi = std::numeric_limits<float>::infinity();
            if (opset_ < 11) {
                lo = node.attrFloat("min", lo);
                hi = node.attrFloat("max", hi);
            } else {
                if (need(1)) {
                    if (!in[1]->isFloat || in[1]->Size() != 1) return fail(err, "Clip: scalar min only");
                    lo = in[1]->f[0];
                }
                if (need(2)) {
                    if (!in[2]->isFloat || in[2]->Size() != 1) return fail(err, "Clip: scalar max only");
                    hi = in[2]->f[0];
                }
            }
            ok = unary(*in[0], *y, [=](float a) { return std::min(hi, std::max(lo, a)); }, err);
        } else if (op == "GlobalAveragePool") {
            ok = globalAvg(*in[0], *y, err);
        } else if (op == "AveragePool") {
            ok = avgPool(node, *in[0], *y, err);
        } else if (op == "MatMul") {
            ok = need(1) && matMul(*in[0], *in[1], *y, err);
        } else if (op == "Softmax") {
            ok = softmax(node, opset_, *in[0], *y, err);
        } else if (op == "ReduceMean") {
            ok = reduceMean(node, *in[0], *y, err);
        } else if (op == "Transpose") {
            ok = transpose(node, *in[0], *y, err);
        } else if (op == "Reshape") {
            ok = need(1) && reshape(*in[0], *in[1], *y, err);
        } else if (op == "Squeeze") {
            ok = squeeze(node, *in[0], *y, err);
        } else if (op == "Slice") {
            ok = slice(in, *y, err);
        } else if (op == "Concat") {
            ok = concat(node, in, *y, err);
        } else if (op == "Shape") {
            y->isFloat = false;
            y->shape = {static_cast<int64_t>(in[0]->shape.size())};
            y->i = in[0]->shape;
        } else if (op == "Cast") {
            ok = cast(node, *in[0], *y, err);
        } else {
            return fail(err, "unsupported operator " + op);
        }
        if (!ok) {
            if (err != nullptr && err->find(op) == std::string::npos) *err = op + ": " + *err;
            return false;
        }
        vals[static_cast<size_t>(node.out[0])] = std::move(y);
        for (int v : node.in)
            if (v >= 0 && lastUse_[static_cast<size_t>(v)] == static_cast<int>(n) && constants_[static_cast<size_t>(v)] == nullptr &&
                v != output_)
                vals[static_cast<size_t>(v)].reset();
    }
    const Tensor* y = vals[static_cast<size_t>(output_)].get();
    if (y == nullptr || !y->isFloat) return fail(err, "model output missing");
    out = y->f;
    shape = y->shape;
    return true;
}

}  // namespace nt::ocr
