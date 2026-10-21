#!/bin/bash
# Host-only test of the production jni.cpp (tests/jni_host_test.cpp: fake JNIEnv/JavaVM/
# AHardwareBuffer, repo assets): native-core audit fixes NC-01/02/03/06/07/08/09.
# Needs a jni.h: JNI_INCLUDE, $JAVA_HOME/include, the default JDK 17 or the NDK sysroot.
# Optional HOSTCHECK_OUT reuses a build; EXTRA_CXXFLAGS/EXTRA_LDFLAGS as in native_fixes_test.sh;
# NT_JNI_TEST_FRAME=<game frame.png> makes the calibration check compare real bead geometry.
set -euo pipefail
CPP="$(cd "$(dirname "$0")/.." && pwd)"
REPO="$(cd "$CPP/../../../../.." && pwd)"
if [ -z "${HOSTCHECK_OUT:-}" ]; then
    HOSTCHECK_OUT="$(mktemp -d "${TMPDIR:-/tmp}/nt-jni-host.XXXXXX")"
    trap 'rm -rf "$HOSTCHECK_OUT"' EXIT
fi
export HOSTCHECK_OUT
JNI_INCLUDE="${JNI_INCLUDE:-}"
for d in "${JAVA_HOME:-}/include" "$HOME/.local/opt/jdk-17.0.20.1+1/include" \
         "$(ls -d "${ANDROID_HOME:-$HOME/Android/Sdk}"/ndk/*/toolchains/llvm/prebuilt/linux-x86_64/sysroot/usr/include 2>/dev/null | tail -1)"; do
    [ -z "$JNI_INCLUDE" ] && [ -f "$d/jni.h" ] && JNI_INCLUDE="$d"
done
[ -n "$JNI_INCLUDE" ] || { echo "no jni.h (set JNI_INCLUDE or JAVA_HOME)" >&2; exit 2; }
JNI_FLAGS="-I $JNI_INCLUDE"
[ -d "$JNI_INCLUDE/linux" ] && JNI_FLAGS="$JNI_FLAGS -I $JNI_INCLUDE/linux"
bash "$REPO/android/tools/hostcheck.sh"
FLAGS="-std=c++17 -O2 -g -fno-fast-math -ffp-contract=off -pthread ${EXTRA_CXXFLAGS:-} -I $CPP/tests/jnihost $JNI_FLAGS -I $CPP/include"
"${CXX:-g++}" $FLAGS -Wall -Wextra -Wno-unused-function -c "$CPP/jni.cpp" -o "$HOSTCHECK_OUT/jni_host.o"
"${CXX:-g++}" $FLAGS -w -c "$CPP/tests/jni_host_test.cpp" -o "$HOSTCHECK_OUT/jni_host_test.o"
"${CXX:-g++}" -pthread ${EXTRA_CXXFLAGS:-} "$HOSTCHECK_OUT/jni_host_test.o" "$HOSTCHECK_OUT/jni_host.o" \
    "$HOSTCHECK_OUT/libnarutocore_host.a" ${EXTRA_LDFLAGS:-} -o "$HOSTCHECK_OUT/jni_host_test"
export ASAN_OPTIONS="${ASAN_OPTIONS:-detect_stack_use_after_return=1:abort_on_error=1}"
export UBSAN_OPTIONS="${UBSAN_OPTIONS:-halt_on_error=1:print_stacktrace=1}"
"$HOSTCHECK_OUT/jni_host_test" "$REPO"
"$HOSTCHECK_OUT/jni_host_test" "$REPO" memory
# NC-06: exit() while frames are being analysed, a few times (a use-after-free aborts the run).
for i in 1 2 3 4; do
    ASAN_OPTIONS="$ASAN_OPTIONS:detect_leaks=0" "$HOSTCHECK_OUT/jni_host_test" "$REPO" exitrace
done
echo "jni_host_test.sh: all modes passed"
