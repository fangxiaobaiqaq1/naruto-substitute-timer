// Only assets/PNG I/O is borrowed from the existing host tool. The production
// jni.cpp is compiled separately and owns JNI_OnLoad, the pipeline and encoding.
#define main unused_hostcheck_main
#include "../hostcheck.cpp"
#undef main
#include <jni.h>
#include <stdexcept>

namespace {
std::string pathString(JNIEnv* env, jstring path) {
    const char* chars = env->GetStringUTFChars(path, nullptr);
    if (!chars) throw std::runtime_error("cannot read host path");
    std::string out(chars);
    env->ReleaseStringUTFChars(path, chars);
    return out;
}
void fail(JNIEnv* env, const std::exception& error) {
    jclass cls = env->FindClass("java/lang/IllegalStateException");
    if (cls) env->ThrowNew(cls, error.what());
}
}  // namespace

extern "C" JNIEXPORT void JNICALL
Java_hostchain_HostPixels_registerAssets(JNIEnv* env, jclass, jstring repo) {
    try {
        registerAssets(pathString(env, repo));
        if (g_images == 0 || g_texts == 0) throw std::runtime_error("missing recognition assets");
        std::fprintf(stderr, "host assets: %d images, %d texts\n", g_images, g_texts);
    } catch (const std::exception& e) { fail(env, e); }
}

// A null target queries dimensions; otherwise copy decoded RGBA to JVM-owned
// direct storage. No analysis result, flags, bean counts or timestamps are made here.
extern "C" JNIEXPORT jintArray JNICALL
Java_hostchain_HostPixels_readPng(JNIEnv* env, jclass, jstring path, jobject target) {
    try {
        nt::RGBA image;
        if (!loadFrame(pathString(env, path), image)) throw std::runtime_error("PNG decode failed");
        jint size[] = {image.Bounds().Dx(), image.Bounds().Dy()};
        if (target) {
            auto* dest = static_cast<uint8_t*>(env->GetDirectBufferAddress(target));
            jlong need = static_cast<jlong>(size[0]) * size[1] * 4;
            if (!dest || env->GetDirectBufferCapacity(target) < need)
                throw std::runtime_error("PNG target is not a large enough direct buffer");
            for (int y = 0; y < size[1]; ++y)
                std::memcpy(dest + static_cast<size_t>(y) * size[0] * 4, image.RowPtr(y), size[0] * 4);
        }
        jintArray result = env->NewIntArray(2);
        if (result) env->SetIntArrayRegion(result, 0, 2, size);
        return result;
    } catch (const std::exception& e) { fail(env, e); return nullptr; }
}
