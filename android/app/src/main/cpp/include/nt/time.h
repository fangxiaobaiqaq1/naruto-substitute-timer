// nt/time.h — Go time.Time / time.Duration 的替身（architect 所有）。
//
// 约定：
//  * 所有时刻都是 CLOCK_MONOTONIC 纳秒（与 Kotlin System.nanoTime() 同一时基）。
//  * TimeNs == 0 等价于 Go 的 time.Time{}.IsZero()。
//  * Go 的 t.Before(u) / t.After(u) / t.Sub(u) / t.Add(d) 直接写成整数比较与加减。
//  * Duration 也是 int64 纳秒；Seconds() 用 SecondsOf(d)。
#pragma once

#include <cstdint>
#include <ctime>

namespace nt {

using TimeNs = int64_t;
using DurationNs = int64_t;

constexpr DurationNs Nanosecond = 1;
constexpr DurationNs Microsecond = 1000 * Nanosecond;
constexpr DurationNs Millisecond = 1000 * Microsecond;
constexpr DurationNs Second = 1000 * Millisecond;

constexpr bool IsZero(TimeNs t) { return t == 0; }
constexpr double SecondsOf(DurationNs d) {
    // Go Duration.Seconds(): sec + nsec/1e9（分两段避免大数精度损失）。
    return static_cast<double>(d / Second) + static_cast<double>(d % Second) / 1e9;
}

// time.Now() 的替身。只在 Go 代码本身调用 time.Now() 的地方使用；
// 凡是 Go 用 capturedAt / at 参数的地方，必须继续用调用方传入的时间。
inline TimeNs NowNs() {
    timespec ts{};
    clock_gettime(CLOCK_MONOTONIC, &ts);
    TimeNs t = static_cast<TimeNs>(ts.tv_sec) * Second + ts.tv_nsec;
    return t == 0 ? 1 : t;  // 0 保留给 "零时刻"
}

}  // namespace nt
