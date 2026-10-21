#!/bin/bash
# Host test of the account-text side rule (tests/hudtext_side_test.cpp: nt/hudtext.h DecideSide).
# Optional HOSTCHECK_OUT reuses a build; EXTRA_CXXFLAGS/EXTRA_LDFLAGS apply to the core and the
# test alike (e.g. ASan/UBSan, see native_fixes_test.sh; use a separate HOSTCHECK_OUT per flag set).
set -euo pipefail
CPP="$(cd "$(dirname "$0")/.." && pwd)"
REPO="$(cd "$CPP/../../../../.." && pwd)"
if [ -z "${HOSTCHECK_OUT:-}" ]; then
    HOSTCHECK_OUT="$(mktemp -d "${TMPDIR:-/tmp}/nt-hudtext-side.XXXXXX")"
    trap 'rm -rf "$HOSTCHECK_OUT"' EXIT
fi
export HOSTCHECK_OUT
bash "$REPO/android/tools/hostcheck.sh"
"${CXX:-g++}" -std=c++17 -O2 -g -fno-fast-math -ffp-contract=off -Wall -Wextra -pthread \
    ${EXTRA_CXXFLAGS:-} -I "$CPP/include" "$CPP/tests/hudtext_side_test.cpp" \
    "$HOSTCHECK_OUT/libnarutocore_host.a" ${EXTRA_LDFLAGS:-} -ldl -o "$HOSTCHECK_OUT/hudtext_side_test"
export ASAN_OPTIONS="${ASAN_OPTIONS:-detect_stack_use_after_return=1:abort_on_error=1}"
export UBSAN_OPTIONS="${UBSAN_OPTIONS:-halt_on_error=1:print_stacktrace=1}"
"$HOSTCHECK_OUT/hudtext_side_test"
