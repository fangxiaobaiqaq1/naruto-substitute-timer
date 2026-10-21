#!/usr/bin/env bash
# Host check of the Android account-text side detection (nt/ocr.h + nt/hudtext.h): the production
# frame::Pipeline with the text reader, the repository's recognizer.onnx, PNG frames. Compare with
# the Windows OCR path on the same frames: android/tools/ocrcheck (Go, needs an ONNX Runtime
# library for this host). No device, Gradle or network.
# Run: android/tools/ocrhost/run.sh [--names 白方小] [--diagnose] [--interval 250] <png or dir>...
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
REPO="$(dirname "$ROOT")"
OUT="${OCRHOST_OUT:-$ROOT/build/ocrhost}"
mkdir -p "$OUT"
HOSTCHECK_OUT="$OUT/hc" bash "$ROOT/tools/hostcheck.sh" >/dev/null
CXX="${CXX:-g++}"
if [ ! -f "$OUT/ocrhost" ] || [ "$HERE/ocrhost.cpp" -nt "$OUT/ocrhost" ] || [ "$OUT/hc/libnarutocore_host.a" -nt "$OUT/ocrhost" ]; then
  $CXX -std=c++17 -O2 -fno-fast-math -ffp-contract=off -w -pthread -msse4.1 -msse4.2 -mpopcnt \
    -I "$ROOT/app/src/main/cpp/include" -I "$ROOT/tools" -DREPO_ROOT="\"$REPO\"" \
    "$HERE/ocrhost.cpp" -Wl,--whole-archive "$OUT/hc/libnarutocore_host.a" -Wl,--no-whole-archive -o "$OUT/ocrhost"
fi
exec "$OUT/ocrhost" "$@"
