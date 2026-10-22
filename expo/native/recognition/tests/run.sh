#!/usr/bin/env bash
set -euo pipefail
recognition="$(cd "$(dirname "$0")/.." && pwd)"
build="$(mktemp -d "${TMPDIR:-/tmp}/nt-ios-recognition.XXXXXX")"
trap 'rm -rf "$build"' EXIT
cmake_bin="${CMAKE:-cmake}"
# This development host has CMake in the Android SDK, not on PATH.
if ! command -v "$cmake_bin" >/dev/null 2>&1 && [[ -x "$HOME/Android/Sdk/cmake/3.22.1/bin/cmake" ]]; then
  cmake_bin="$HOME/Android/Sdk/cmake/3.22.1/bin/cmake"
fi
"$cmake_bin" -S "$recognition" -B "$build" -DCMAKE_BUILD_TYPE=Release
"$cmake_bin" --build "$build" --target timer_session_test match_kernels_test -j "${JOBS:-4}"
"$build/timer_session_test"
"$build/match_kernels_test"
if [[ "$(uname -s)" == Darwin ]]; then
  xcrun clang++ -std=c++17 -fobjc-arc -UNDEBUG "$recognition/tests/PixelOrientationTest.mm" \
    -framework Foundation -framework CoreImage -framework CoreVideo -framework CoreGraphics \
    -o "$build/pixel_orientation_test"
  "$build/pixel_orientation_test"
fi
