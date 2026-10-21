#!/bin/bash
# Host-only tests; no APK, assets, or Go source changes. Optional HOSTCHECK_OUT reuses a build.
set -euo pipefail
CPP="$(cd "$(dirname "$0")/.." && pwd)"
REPO="$(cd "$CPP/../../../../.." && pwd)"
if [ -z "${HOSTCHECK_OUT:-}" ]; then
    HOSTCHECK_OUT="$(mktemp -d "${TMPDIR:-/tmp}/nt-hud-start.XXXXXX")"
    trap 'rm -rf "$HOSTCHECK_OUT"' EXIT
fi
export HOSTCHECK_OUT
bash "$REPO/android/tools/hostcheck.sh"
"${CXX:-g++}" -std=c++17 -O3 -g -fno-fast-math -ffp-contract=off -Wall -Wextra -pthread \
    ${EXTRA_CXXFLAGS:-} -I "$CPP/include" "$CPP/tests/hud_start_test.cpp" \
    "$HOSTCHECK_OUT/libnarutocore_host.a" ${EXTRA_LDFLAGS:-} -o "$HOSTCHECK_OUT/hud_start_test"
"$HOSTCHECK_OUT/hud_start_test"
