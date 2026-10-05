cmake_minimum_required(VERSION 3.22.1)
project(narutocore LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# 识别核心：src/ 下所有 .cpp（每个 Go 包一个子目录）+ JNI 边界 jni.cpp。
file(GLOB_RECURSE NT_CORE_SOURCES CONFIGURE_DEPENDS "${CMAKE_CURRENT_SOURCE_DIR}/src/*.cpp")

add_library(narutocore SHARED ${NT_CORE_SOURCES} jni.cpp)

target_include_directories(narutocore PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/include")

# 与 Go 版逐位一致的前提：
#  - 不开 -ffast-math（NaN/舍入/结合律都要按 IEEE 走）；
#  - -ffp-contract=off：禁止编译器把 a*b+c 融合成 FMA（Go amd64 不融合，阈值边界会差 1ulp）；
#  - Release/Debug 一律 -O3，识别循环每帧都在热路径上。
target_compile_options(narutocore PRIVATE
    -O3
    -fno-fast-math
    -ffp-contract=off
    -fvisibility=hidden
    -fvisibility-inlines-hidden
    -fexceptions
    -frtti
    -Wall
    -Wno-unused-function
)

if(ANDROID_ABI STREQUAL "x86_64")
    # Android x86_64 ABI 保证 SSE4.2/POPCNT；NCC 点积靠它向量化。
    target_compile_options(narutocore PRIVATE -msse4.1 -msse4.2 -mpopcnt)
endif()

# 去掉未用符号并只导出 JNI 入口（JNIEXPORT 带 default visibility）。
target_link_options(narutocore PRIVATE -Wl,--gc-sections -Wl,--exclude-libs,ALL)

find_library(log-lib log)
find_library(android-lib android)
find_library(nativewindow-lib nativewindow)
find_library(jnigraphics-lib jnigraphics)

target_link_libraries(narutocore
    ${android-lib}        # AHardwareBuffer_fromHardwareBuffer
    ${nativewindow-lib}   # AHardwareBuffer_lock / unlock / describe
    ${jnigraphics-lib}
    ${log-lib}
)
