#pragma once
// Host stand-in for <android/asset_manager.h>: no assets (jni.cpp nativeSetTextModel fails cleanly).
#include <cstddef>
#include <cstdint>
#include <sys/types.h>
struct AAssetManager;
struct AAsset;
constexpr int AASSET_MODE_UNKNOWN = 0;
constexpr int AASSET_MODE_BUFFER = 3;
inline AAsset* AAssetManager_open(AAssetManager*, const char*, int) { return nullptr; }
inline const void* AAsset_getBuffer(AAsset*) { return nullptr; }
inline off64_t AAsset_getLength64(AAsset*) { return 0; }
inline void AAsset_close(AAsset*) {}
