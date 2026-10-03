// nt/json.h — JSON 读取（architect 所有）。
// 使用 vendored nlohmann/json v3.11.3（MIT，单头文件，include/third_party/nlohmann/json.hpp）。
// 用于 templates/manifest.json、avatars/index.json 等资源文本（从 AssetStore::text 取）。
//
// 用法：
//   auto doc = nt::json::parse(*text, nullptr, /*allow_exceptions=*/false);
//   if (doc.is_discarded()) { ...解析失败... }
//   double v = nt::JsonNumber(doc, "threshold", 0);
//
// 注意：Go encoding/json 对缺失字段给零值；下面的 helper 同样在缺失/类型不符时
// 返回调用方给的默认值，而不是抛异常。
#pragma once

#include <string>

#include "third_party/nlohmann/json.hpp"

namespace nt {

using json = nlohmann::json;

inline double JsonNumber(const json& obj, const char* key, double def = 0) {
    auto it = obj.find(key);
    if (it == obj.end() || !it->is_number()) return def;
    return it->get<double>();
}
inline int JsonInt(const json& obj, const char* key, int def = 0) {
    auto it = obj.find(key);
    if (it == obj.end() || !it->is_number()) return def;
    return it->get<int>();
}
inline bool JsonBool(const json& obj, const char* key, bool def = false) {
    auto it = obj.find(key);
    if (it == obj.end() || !it->is_boolean()) return def;
    return it->get<bool>();
}
inline std::string JsonString(const json& obj, const char* key, const std::string& def = std::string()) {
    auto it = obj.find(key);
    if (it == obj.end() || !it->is_string()) return def;
    return it->get<std::string>();
}

}  // namespace nt
