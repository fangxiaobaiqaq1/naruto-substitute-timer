// nt/hudtext.h — 对局顶部账号文字认边（Android 端；Port of internal/hudtext 的账号部分）。
//
// Windows（Go internal/hudtext）在对局画面按豆排位置裁出两侧标题条（忍者名[版本](账号)），
// 后台本机 PP-OCRv4 读三种处理（原色 ×2、增强对比 ×2、原色 ×4），两种以上一致、两次不同采集时间
// 重复、当前字形像素仍吻合，才认这个账号；「我的名字」只在一侧出现 → 我方在那一侧；两侧都不是我的名字
// （我方被挡住）时，只有一侧读出不是我的名字的完整账号 → 那是对面，我方在另一侧（DecideSide，两端同一规则）。
// 这里逐行移植同样的标题条几何、三种处理、行识别（nt/ocr.h）、分词、字形证据、两次确认、
// 节奏（缺边每 400 ms、已认出每 2 s 复查、失败退避 5 s），只取账号（不认忍者名：手机端忍者名仍走
// 模板与头像）。手机识别慢（模拟器上一次 1–2 s）：结果作废时限 2 s → 4 s、单个任务超时 3 s → 4 s；
// 迟到的结果照旧要对上**当前帧**的字形像素（或标题条像素没变）才算数。第三种处理只在它能改变结果时
// 才读（ReadAccounts，结果与全读逐位相同）。Kotlin 只在「自动认边」且我方未知时打开（Session
// .wantsTextRecognition），所以认出后不再复查。
//
// 与 Windows 不同的地方（都因为手机画面不同，另见 docs/安卓原生开发.md §6.1）：
//  * 手机录屏里有我们自己的悬浮卡片，常压在标题条末尾（「漩涡鸣人(白方小)对面·待认边」），
//    所以账号取标题里**最后一对完整括号**，而不是要求括号在行尾。
//  * 支援忍者头像会盖住账号末尾（「宇智波佐助[侠隐江湖](白方」）：行尾只有左括号时，看得见的部分
//    （≥ 2 字且 ≥ 名字一半）与「我的名字」开头逐字相同，算「被遮住的我方」；完整读出的账号优先。
//  * 「我的名字」里 OCR 字典没有的字（装饰符号，如 ღ）只能读成别的字或读不出：它匹配 0 或 1 个
//    任意字；字典里有的字必须逐字相同（白/自 不通用）。
//  * 排除法本身与 Windows 相同（用户决定：名字被挡住要看对面的名字反推）：「不是我」的一侧必须是
//    完整账号（有右括号、ValidAccount）且连被遮住的开头都对不上我的名字。代价也相同：「我的名字」填错
//    （不在这局）时，一侧完整读出、另一侧没读出就会认出一边，可能认错。结果仍可能与 Windows 不同，因为
//    读出的账号不同：卡片接在后面的左侧账号手机常能读完整（第一条），Windows 读不出。视频 16 填张三：
//    Windows 只读出右侧，一直认左；手机只在左侧还没读完整时认左，读完整后两侧都不是我 → 不认边。
//  * 引擎未确认「对局」但两侧豆排完整可读（决斗时回合标志被我们的卡片盖住）也读。
//  * 同一几何下 ≤ 2 s 的看不清（保持）不丢掉还在等第二次的读法和正在读的任务（Go 会丢）：手机上
//    卡片让对局每一两秒就「看不清」一帧，一次识别又要 1–2 s，按 Go 的规则永远凑不齐两次。
//    读法仍要对上保持期间每一帧和之后第一帧的字形像素。
//
// 线程：Engine 只在分析线程调用（JNI g_analysisMu）；识别在 ocr 后台线程（一次最多一个任务），
// 结果经带锁的单槽交回，分析线程从不等待识别。
#pragma once

#include <array>
#include <atomic>
#include <condition_variable>
#include <cstdint>
#include <functional>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

#include "nt/config.h"
#include "nt/engine.h"
#include "nt/image.h"
#include "nt/ocr.h"
#include "nt/time.h"

namespace nt::hudtext {

constexpr DurationNs requestInterval = 400 * Millisecond;
constexpr DurationNs refreshInterval = 2 * Second;
constexpr DurationNs resultMaxAge = 4 * Second;  // Go 2 s
constexpr DurationNs retryDelay = 5 * Second;
constexpr DurationNs jobTimeout = 4 * Second;  // Go 3 s
constexpr DurationNs textPauseTTL = 2 * Second;
constexpr int variants = 3;

// ---------------------------------------------------------------------------
// 纯函数（主机测试与 Go 对照用）
// ---------------------------------------------------------------------------

// Go nameRegions：两侧标题条（采集像素）。false = 当前几何不支持。
class NameLayout {
public:
    explicit NameLayout(const config::LayoutConfig& cfg);
    bool Ok() const { return ok_; }
    bool Regions(const RGBA* img, const std::string& profile, std::array<Rect, 2>& out) const;

private:
    config::LayoutConfig cfg_;
    detect::ContentMode mode_ = detect::ModeAuto;
    std::shared_ptr<engine::ConfiguredLayout> layout_;
    bool ok_ = false;
};

// Go lettering：白字黑边的像素签名（这里是 64 位哈希，只比相等）与像素数。
uint64_t Lettering(const RGBA& img, Rect roi, int& white);

// Go buildSheet 的一行：side、variant，以及它在 Go 拼版图里的位置（字坐标都按它算）。
struct Row {
    int side = 0, variant = 0;
    Rect rect;
    RGBA image;  // 这一行的像素（与 Go 拼版图里 rect 内的像素逐字节相同），原点 (0,0)
};
// strips[i] 为空图 = 这一侧没有。
std::vector<Row> BuildRows(const std::array<RGBA, 2>& strips);

// Go wordsForRow / sheet.texts。
std::vector<ocr::Word> WordsForRow(const std::vector<ocr::Line>& lines, const Rect& rect);
std::array<std::array<std::string, variants>, 2> Texts(const std::vector<Row>& rows, const std::vector<ocr::Line>& lines);

// Go compact。
std::string Compact(const std::string& s);

// 一种处理的账号读法：标题里最后一对完整括号内的文字，或者（没有完整括号时）最后一个左括号之后
// 到行尾的可见部分（去掉行尾的 U+FFFD）。Value = "(" + 账号 + ")" 或 "(" + 可见部分；空 = 没有。
std::string AccountSegment(const std::string& raw);
// 三种处理里至少两种相同的段；没有则空。
std::string Consensus(const std::array<std::string, variants>& raw);

// OCR 字典（含 CTC 空白之外的全部字符）判断「我的名字」里哪些字读得出。
using Dictionary = std::function<bool(char32_t)>;
// 段与名字（NormalizeName 后）：2 = 完整相同（装饰字通配），1 = 被遮住的开头相同，0 = 不是。
int MatchName(const std::string& segment, const std::string& name, const Dictionary& known);
// Go validAccount（显示用的对面账号）。
bool ValidAccount(const std::string& s);

// 认边（Go Engine.Analyze 的账号部分）：accounts[i] = 第 i 侧已确认的账号段（AccountSegment 形式，空 = 没有），
// names = 我的名字（没有非空名字 = 不认边）。① 恰好一侧是我的名字（完整相同优先于被遮住的开头；两侧都像我 →
// 不认边）→ 我方在那一侧；② 否则恰好一侧是完整、合法、连开头都不是我的名字的账号 → 它是对面，我方在
// 另一侧（player 留空）。
struct SideDecision {
    int side = -1;           // 0 左、1 右、-1 不认边
    std::string player, opp; // PlayerName（① 才有）、OppName（不是我的完整账号）
    std::string how;         // 日志：account / account start, end covered / other account not mine
};
SideDecision DecideSide(const std::array<std::string, 2>& accounts, const std::vector<std::string>& names,
                        const Dictionary& known);

// ---------------------------------------------------------------------------
// 字形证据（Go glyph_evidence.go）
// ---------------------------------------------------------------------------
struct GlyphSample {
    Point p;
    double value = 0;
};
struct GlyphCell {
    std::vector<GlyphSample> samples;
    double sum = 0, variance = 0;
};
struct GlyphEvidence {
    Rect bounds;
    std::vector<GlyphCell> cells;
    bool Matches(const RGBA* img) const;
};
std::shared_ptr<GlyphEvidence> NewGlyphEvidence(const RGBA& img, Rect bounds);
// Go sheet.evidence：rows 中 side 一侧、读出 expected 的字框 → 标题条 strip 上的证据（整帧坐标）。
std::shared_ptr<GlyphEvidence> Evidence(const std::vector<Row>& rows, const std::vector<ocr::Line>& lines, int side,
                                        const std::string& expected, const RGBA& strip, const Rect& roi);

// 一次识别任务（Go Engine.read 的账号部分）：两侧三种处理、三取二、字形证据。第三种处理只在它能
// 改变结果时才读（前两种的账号段已相同则多数已定），结果与六行全读逐位相同。strips 为不透明裁图，
// regions 为它们在整帧里的位置。
struct ReadResult {
    std::array<std::string, 2> values;
    std::array<std::shared_ptr<GlyphEvidence>, 2> proofs;
    std::array<std::array<std::string, variants>, 2> texts;  // 没读的处理为空串
    int rows = 0;
    double rowMillis = 0;  // 各行耗时之和
    std::string err;
};
bool ReadAccounts(const ocr::Recognizer& recognizer, const std::array<RGBA, 2>& strips, const std::array<Rect, 2>& regions,
                  ReadResult& out, const std::atomic<bool>* cancel = nullptr);

// Go valueState。
struct ValueState {
    std::string candidate, accepted;
    int hits = 0;
    TimeNs at = 0;
    void observe(const std::string& value, TimeNs at);
};
void ObserveBound(ValueState& state, std::shared_ptr<GlyphEvidence>& proof, const std::string& value,
                  const std::shared_ptr<GlyphEvidence>& incoming, const RGBA* img, TimeNs at, bool unchanged);

// ---------------------------------------------------------------------------
// 后台识别（一个线程，一次一个任务；模型按需加载，空闲后释放）
// ---------------------------------------------------------------------------
class Worker {
public:
    // 读模型字节（JNI：APK assets/ocr/recognizer.onnx）。返回 false = 不可用（err 写原因）。
    using Loader = std::function<bool(std::vector<uint8_t>& out, std::string* err)>;
    using Task = std::function<void(const ocr::Recognizer* recognizer, const std::string& loadError)>;

    static Worker& Instance();
    void SetLoader(Loader loader);
    bool HasLoader();
    // 交一个任务；已有任务在排队/执行时返回 false（调用方下一帧再试）。
    bool TrySubmit(Task task);
    // 没有任务时多久释放模型。
    void SetIdleRelease(DurationNs d);
    // 立即释放模型（任务进行中则等它结束后释放）。
    void Release();
    bool Loaded();
    // 没有排队或执行中的任务（主机工具用）。
    bool Idle();
    // 诊断：最近一次加载耗时与权重字节数。
    void Stats(double& loadMillis, size_t& weightBytes);
    // 识别器能输出这个字吗（模型字典；从未加载过时一律 true，即要求逐字相同）。
    bool Known(char32_t r);
    // 诊断日志（JNI：logcat；主机：不设）。在识别线程上调用，不要阻塞。
    void SetLog(std::function<void(const std::string&)> log);
    void Log(const std::string& line);

private:
    Worker() = default;
    void run();
    std::mutex mu_;
    std::condition_variable cv_;
    bool started_ = false;
    bool releaseRequested_ = false;
    Loader loader_;
    Task task_;
    bool busy_ = false;
    DurationNs idle_ = 20 * Second;
    std::unique_ptr<ocr::Recognizer> recognizer_;
    std::vector<char32_t> known_;  // sorted dictionary runes; kept after the model is released
    std::function<void(const std::string&)> log_;
    double loadMillis_ = 0;
    size_t weightBytes_ = 0;
};

// ---------------------------------------------------------------------------
// Engine：包住 Gated 引擎（Go hudtext.Engine，只取账号）
// ---------------------------------------------------------------------------
struct Completion;

class Engine final : public engine::Engine {
public:
    // names：当前「我的名字」（identity::MineNames，已 NormalizeName）。
    Engine(std::shared_ptr<engine::Engine> inner, const config::Config& cfg, std::function<std::vector<std::string>()> names,
           Dictionary known, Worker* worker);
    ~Engine() override;
    engine::Result AnalyzeAt(const RGBA* img, TimeNs at) override;

    void SetEnabled(bool enabled);
    // "off" / "waiting" / "reading" / "pending" / "ready" / "template" / "unavailable"，以及失败原因。
    std::string Status() const;
    std::string LastError() const;
    const std::shared_ptr<engine::Engine>& Inner() const { return inner_; }

    // 主机工具：每次识别的三种处理原文（可空）。在 ocr 线程上调用。
    std::function<void(TimeNs at, const std::array<std::array<std::string, variants>, 2>& views, double millis)> OnRead;

private:
    struct Side {
        uint64_t signature = 0;
        ValueState account;
        std::shared_ptr<GlyphEvidence> accountProof;
    };
    struct Key {
        Rect bounds;
        std::array<Rect, 2> regions;
        std::string profile;
        bool operator==(const Key& o) const { return bounds == o.bounds && regions == o.regions && profile == o.profile; }
        bool operator!=(const Key& o) const { return !(*this == o); }
    };
    struct Mailbox;

    void invalidate();
    void backendFailed(TimeNs at, const std::string& cause);
    bool pauseText(const RGBA* img, const engine::Result& res, TimeNs at);
    void validateTextPixels(const RGBA* img, const std::array<Rect, 2>& regions, std::array<uint64_t, 2>& signatures,
                            std::array<int, 2>& white);
    bool poll(std::shared_ptr<Completion>& out);
    bool hudReadable(const engine::Result& res) const;

    std::shared_ptr<engine::Engine> inner_;
    config::Config cfg_;
    std::function<std::vector<std::string>()> names_;
    Dictionary known_;
    Worker* worker_;
    NameLayout layout_;
    std::shared_ptr<Mailbox> mailbox_;

    bool enabled_ = true;
    bool reported_ = false;  // side logged for this generation
    bool busy_ = false;
    TimeNs busySince_ = 0;
    std::shared_ptr<std::atomic<bool>> cancel_;
    uint64_t generation_ = 0;
    Key key_;
    bool haveKey_ = false;
    TimeNs lastAt_ = 0, nextAt_ = 0, pausedAt_ = 0, retryAt_ = 0;
    std::string namesKey_;
    std::array<Side, 2> sides_;
    mutable std::mutex statusMu_;
    std::string status_ = "waiting", lastError_;
};

}  // namespace nt::hudtext
