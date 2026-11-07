#!/usr/bin/env bash
# Real static VS PNGs + production OCR/Pipeline, plus bounded scheduler/gate unit tests.
# No APK/device/network. Reuse a private build: HOSTCHECK_OUT=/tmp/vs-core .../hudtext_vs_test.sh
set -euo pipefail
CPP="$(cd "$(dirname "$0")/.." && pwd)"
REPO="$(cd "$CPP/../../../../.." && pwd)"
export HOSTCHECK_OUT="${HOSTCHECK_OUT:-$REPO/android/build/vs-side-check/core}"
bash "$REPO/android/tools/hostcheck.sh"
"${CXX:-g++}" -std=c++17 -O2 -g -fno-fast-math -ffp-contract=off -pthread -Wall -Wno-unused-function \
    ${EXTRA_CXXFLAGS:-} -I "$CPP/include" "$CPP/tests/hudtext_vs_test.cpp" \
    "$HOSTCHECK_OUT/libnarutocore_host.a" ${EXTRA_LDFLAGS:-} -o "$HOSTCHECK_OUT/hudtext_vs_test"
"$HOSTCHECK_OUT/hudtext_vs_test" "$REPO"
