#!/bin/bash
# 主机侧构建识别核心静态库（src/**/*.cpp，不含 jni.cpp）+ hostcheck 驱动。
# 用法：tools/hostcheck.sh [hostcheck 参数...]   输出目录：android/build/hostcheck
set -e
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(dirname "$HERE")"
SRC="$ROOT/app/src/main/cpp"
OUT="${HOSTCHECK_OUT:-$ROOT/build/hostcheck}"
OBJ="$OUT/obj"
mkdir -p "$OBJ"
CXX="${CXX:-g++}"
# 与 CMakeLists.txt 相同的数值语义：禁止 fast-math，禁止 FMA 融合。
FLAGS="-std=c++17 -O3 -g -fno-fast-math -ffp-contract=off -Wall -Wno-unused-function -pthread -I $SRC/include $EXTRA_CXXFLAGS"
export CXX FLAGS OBJ SRC
find "$SRC/src" -name '*.cpp' | sort | xargs -P "$(nproc)" -I{} sh -c '
  f={}; o="$OBJ/$(basename $(dirname $f))_$(basename $f .cpp).o"
  if [ ! -f "$o" ] || [ "$f" -nt "$o" ] || [ -n "$(find "$SRC/include" -newer "$o" -print -quit)" ]; then
    $CXX $FLAGS -c "$f" -o "$o" || exit 255
  fi'
rm -f "$OUT/libnarutocore_host.a"
ar rcs "$OUT/libnarutocore_host.a" "$OBJ"/*.o
if [ ! -f "$OUT/hostcheck.o" ] || [ "$HERE/hostcheck.cpp" -nt "$OUT/hostcheck.o" ]; then
  $CXX $FLAGS -w -I "$HERE" -c "$HERE/hostcheck.cpp" -o "$OUT/hostcheck.o"
fi
# --whole-archive：确认所有翻译单元的符号都能解析（无未定义 / 重复定义）。
$CXX -pthread $EXTRA_LDFLAGS -o "$OUT/hostcheck" "$OUT/hostcheck.o" -Wl,--whole-archive "$OUT/libnarutocore_host.a" -Wl,--no-whole-archive
if [ $# -gt 0 ]; then exec "$OUT/hostcheck" "$@"; fi
