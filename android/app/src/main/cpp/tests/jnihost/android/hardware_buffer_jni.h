#pragma once
// Host stand-in for <android/hardware_buffer_jni.h>.
#include <jni.h>
#include "hardware_buffer.h"
extern "C" AHardwareBuffer* AHardwareBuffer_fromHardwareBuffer(JNIEnv* env, jobject hardwareBufferObj);
