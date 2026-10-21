#pragma once
// Host stand-in for <android/log.h>: messages go to stderr when NT_JNI_HOST_LOG is set.
#include <cstdarg>
#include <cstdio>
#include <cstdlib>
constexpr int ANDROID_LOG_INFO = 4;
constexpr int ANDROID_LOG_ERROR = 6;
inline int __android_log_print(int, const char* tag, const char* fmt, ...) {
    if (std::getenv("NT_JNI_HOST_LOG") == nullptr) return 0;
    std::fprintf(stderr, "[%s] ", tag);
    va_list args;
    va_start(args, fmt);
    int n = std::vfprintf(stderr, fmt, args);
    va_end(args);
    std::fputc('\n', stderr);
    return n;
}
