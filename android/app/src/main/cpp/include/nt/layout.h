// nt/layout.h — internal/layout（owner: cpp-match-detect；实现 src/layout/transform.cpp）
// 生产路径未使用（Go 也只在测试/工具里用），按原样移植以保持包完整。
#pragma once

#include <string>

#include "nt/image.h"

namespace nt::layout {

// Go: type Mode string
using Mode = std::string;
inline const Mode Auto = "auto";
inline const Mode Stretch = "stretch";
inline const Mode Letterbox = "letterbox";

struct Transform {
    Rect Client;
    Rect Content;
    Rect Capture;
    Point Reference;

    Point ReferenceToClient(Point p) const;
    Point ClientToReference(Point p) const;
    Point ClientToCapture(Point p) const;
    Point CaptureToClient(Point p) const;
    Point ReferenceToCapture(Point p) const;
    Point NormalizedContentToCapture(double x, double y) const;
};

// 返回 false 等价于 Go 的 err != nil；err 可为 nullptr。
bool New(const Rect& client, const Rect& capture, Point reference, const Mode& mode, double aspectTolerance,
         Transform& out, std::string* err = nullptr);

}  // namespace nt::layout
