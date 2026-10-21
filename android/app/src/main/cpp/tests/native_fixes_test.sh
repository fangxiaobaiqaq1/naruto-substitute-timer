#!/bin/bash
# Host-only tests of the native-core audit fixes NC-01/02/04/05/09 (tests/native_fixes_test.cpp).
# Optional HOSTCHECK_OUT reuses a build; EXTRA_CXXFLAGS/EXTRA_LDFLAGS apply to the core and the
# test alike, e.g. EXTRA_CXXFLAGS="-O1 -fsanitize=address,undefined -fno-omit-frame-pointer"
# EXTRA_LDFLAGS=-fsanitize=address,undefined (use a separate HOSTCHECK_OUT per flag set).
set -euo pipefail
CPP="$(cd "$(dirname "$0")/.." && pwd)"
REPO="$(cd "$CPP/../../../../.." && pwd)"
if [ -z "${HOSTCHECK_OUT:-}" ]; then
    HOSTCHECK_OUT="$(mktemp -d "${TMPDIR:-/tmp}/nt-native-fixes.XXXXXX")"
    trap 'rm -rf "$HOSTCHECK_OUT"' EXIT
fi
export HOSTCHECK_OUT
bash "$REPO/android/tools/hostcheck.sh"
"${CXX:-g++}" -std=c++17 -O2 -g -fno-fast-math -ffp-contract=off -Wall -Wextra -pthread \
    ${EXTRA_CXXFLAGS:-} -I "$CPP/include" "$CPP/tests/native_fixes_test.cpp" \
    "$HOSTCHECK_OUT/libnarutocore_host.a" ${EXTRA_LDFLAGS:-} -ldl -o "$HOSTCHECK_OUT/native_fixes_test"
export ASAN_OPTIONS="${ASAN_OPTIONS:-detect_stack_use_after_return=1:abort_on_error=1}"
export UBSAN_OPTIONS="${UBSAN_OPTIONS:-halt_on_error=1:print_stacktrace=1}"
"$HOSTCHECK_OUT/native_fixes_test"
# NC-05: each of the pool's (up to 3) worker threads failing to start, one process each.
for n in 1 2 3; do FAIL_NTH=$n "$HOSTCHECK_OUT/native_fixes_test" pool-threads; done
