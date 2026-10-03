// nt/identity.h — internal/identity（owner: cpp-identity；实现 src/identity/{identity,glyph_mask}.cpp）
//
// 账号名图鉴（用户文件，Kotlin 从 filesDir 注册进 AssetStore）：
//   "identity/<名字>[_后缀].png|jpg|jpeg"        → VS 底栏（scene "vs"）；只取直接子文件
//   "identity/seen/<...>.png"                   → VS 底栏（曾经裁过的对面）
//   "identity/fight/<...>.png"                  → 对局顶部 HUD（scene "fight"）
// 我方名字：SetMineNames（Kotlin 设置页 → JNI nativeSetPlayerNames）。
// saveOppCrop：C++ 不写文件。裁剪结果放进待取队列，由 JNI nativeTakeOppCrop 交给
// Kotlin 编码 PNG 写到 filesDir/identity/seen/opp-<yyyyMMdd-HHmmss>.png，并重新注册进 AssetStore。
#pragma once

#include <functional>
#include <memory>
#include <string>
#include <vector>

#include "nt/assets.h"
#include "nt/config.h"
#include "nt/detect.h"
#include "nt/image.h"
#include "nt/time.h"

namespace nt::identity {

inline const std::string AssetDir = "identity/";
inline const std::string SeenDir = "identity/seen/";
inline const std::string FightDir = "identity/fight/";
constexpr double minScore = 0.72;
constexpr double minGap = 0.08;
constexpr double fightMinScore = 0.80;
constexpr DurationNs recognitionInterval = 500 * Millisecond;

// SetMineNames 设置里改我方名字后立刻用于认边，不用重启（线程安全）。
void SetMineNames(const std::vector<std::string>& names);
// MineNames returns an owned snapshot.
std::vector<std::string> MineNames();

struct sideROIs {
    config::NormalizedRect Left, Right;
};
// DefaultROIs 是 VS / 死亡换人底栏左右账号区域（内容区归一化）。
extern const sideROIs DefaultROIs;
// FightROIs contain the account names in the top battle HUD, not VS's footer.
extern const sideROIs FightROIs;

// Named 是一张账号名模板。Image 为解码后的资源图（非预乘；灰度用 match::ToGrayAsset）。
struct Named {
    std::string Name;
    std::shared_ptr<const AssetImage> Image;
    bool Mine = false;
    std::string Scene;  // "fight" for top HUD crops; empty/"vs" for footer crops
    // C++ 新增（纯加速，不改结果）：LoadBook 预先算好的 match.ToGray(Image)。
    // Go 在 bestHit 里每次重算；为空时 bestHit 现算，结果逐位相同。
    std::shared_ptr<const Gray> GrayCache;
};

// Readout 是一次 VS / 换人 / 对局画面上的认人结果。
struct Readout {
    std::string Side;  // left / right / ""
    std::string Mine;
    std::string Opp;
};

// LoadBook 读账号名图鉴（来自 AssetStore 的 identity/ 前缀）。
std::vector<Named> LoadBook(const config::Config& cfg);
std::string labelFromFile(const std::string& name);

using GuessFunc = std::function<Readout(const RGBA* img, const std::string& scene)>;
// Guesser recognizes accounts only in confirmed VS or battle scenes. Account
// changes (SetMineNames revision) reload the book.
GuessFunc Guesser(const config::Config& cfg);

// Read 比较左右底栏和名册。
Readout Read(const RGBA* img, const std::vector<Named>& book, detect::ContentMode mode);
// ReadFight recognizes account-name-only templates from the battle HUD.
Readout ReadFight(const RGBA* img, const std::vector<Named>& book, detect::ContentMode mode);

std::string opposite(const std::string& side);
// NormalizeName 去掉空白和路径字符，方便当文件名、当配置项。
std::string NormalizeName(const std::string& s);

// HUD name patches: glyph + nearby outline mask. nullptr-equivalent（Nil()）when seeds < 12.
Gray accountGlyphMask(const Gray& templ);

// saveOppCrop 的 Android 版：节流 8 秒，裁 DefaultROIs 对应侧，放入待取槽（只保留最新一张）。
void saveOppCrop(const RGBA* img, const std::string& side, detect::ContentMode mode);
// JNI 取走待保存的对面账号裁剪（紧凑 RGBA，原点 0,0）。没有则返回 false。
bool TakePendingOppCrop(RGBA& out);

}  // namespace nt::identity
