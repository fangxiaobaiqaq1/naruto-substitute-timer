#!/bin/bash
# Host-only randomized equivalence tests of the optimized kernels (docs/性能优化-android-native.md).
# Optional HOSTCHECK_OUT reuses a hostcheck build.
set -euo pipefail
CPP="$(cd "$(dirname "$0")/.." && pwd)"
REPO="$(cd "$CPP/../../../../.." && pwd)"
if [ -z "${HOSTCHECK_OUT:-}" ]; then
    HOSTCHECK_OUT="$(mktemp -d "${TMPDIR:-/tmp}/nt-perf-kernels.XXXXXX")"
    trap 'rm -rf "$HOSTCHECK_OUT"' EXIT
fi
export HOSTCHECK_OUT
bash "$REPO/android/tools/hostcheck.sh"
"${CXX:-g++}" -std=c++17 -O2 -g -fno-fast-math -ffp-contract=off -Wall -Wextra -pthread \
    ${EXTRA_CXXFLAGS:-} -I "$CPP/include" "$CPP/tests/perf_kernels_test.cpp" \
    "$HOSTCHECK_OUT/libnarutocore_host.a" ${EXTRA_LDFLAGS:-} -o "$HOSTCHECK_OUT/perf_kernels_test"
"$HOSTCHECK_OUT/perf_kernels_test"
