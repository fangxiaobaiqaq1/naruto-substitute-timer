#pragma once
// Host stand-in for <android/asset_manager_jni.h>.
#include <jni.h>
#include "asset_manager.h"
inline AAssetManager* AAssetManager_fromJava(JNIEnv*, jobject) { return nullptr; }
