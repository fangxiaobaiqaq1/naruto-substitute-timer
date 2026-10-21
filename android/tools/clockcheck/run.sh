#!/usr/bin/env bash
# Go/Kotlin parity of SideClock: replays scenarios.in through internal/app.SideClock
# (main.go) and the production tracker/SideClock.kt (ClockCheck.kt) and compares the
# per-step state lines byte for byte. Host JVM only: no Gradle, network, device or APK.
# Kotlin 2.0.21 comes from the Gradle cache, like chaincheck.
# Run from anywhere: android/tools/clockcheck/run.sh [scenarios.in]
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
KT="$REPO/android/app/src/main/java/com/narutotimer"
SCENARIOS="$(cd "$(dirname "${1:-$HERE/scenarios.in}")" && pwd)/$(basename "${1:-$HERE/scenarios.in}")"
JAVA_HOME="${JAVA_HOME:-$HOME/.local/opt/jdk-17.0.20.1+1}"
CACHE="${GRADLE_USER_HOME:-$HOME/.gradle}/caches/modules-2/files-2.1"
[[ -x "$JAVA_HOME/bin/java" ]] || { echo "Need JDK17 (JAVA_HOME)" >&2; exit 1; }
command -v go >/dev/null || { echo "Need go on PATH" >&2; exit 1; }
jar() {
    local result
    result="$(find "$CACHE/$1/$2/$3" -name "$2-$3.jar" -print -quit)"
    [[ -n "$result" ]] || { echo "Missing cached $2:$3" >&2; exit 1; }
    printf '%s' "$result"
}
STDLIB="$(jar org.jetbrains.kotlin kotlin-stdlib 2.0.21)"
ANNOTATIONS="$(jar org.jetbrains annotations 13.0)"
COMPILER="$(jar org.jetbrains.kotlin kotlin-compiler-embeddable 2.0.21):$STDLIB:$ANNOTATIONS"
COMPILER+=":$(jar org.jetbrains.kotlin kotlin-script-runtime 2.0.21)"
COMPILER+=":$(jar org.jetbrains.kotlin kotlin-reflect 1.6.10)"
COMPILER+=":$(jar org.jetbrains.kotlin kotlin-daemon-embeddable 2.0.21)"
COMPILER+=":$(jar org.jetbrains.intellij.deps trove4j 1.0.20200330)"
COMPILER+=":$(jar org.jetbrains.kotlinx kotlinx-coroutines-core-jvm 1.6.4)"
OUT="$(mktemp -d /tmp/naruto-clockcheck.XXXXXX)"
trap 'rm -rf "$OUT"' EXIT
"$JAVA_HOME/bin/java" -cp "$COMPILER" org.jetbrains.kotlin.cli.jvm.K2JVMCompiler \
    -jvm-target 17 -no-stdlib -no-reflect -classpath "$STDLIB:$ANNOTATIONS" -d "$OUT/classes" \
    "$KT/tracker/SideClock.kt" "$HERE/ClockCheck.kt"
"$JAVA_HOME/bin/java" -cp "$OUT/classes:$STDLIB" clockcheck.ClockCheckKt "$SCENARIOS" > "$OUT/kotlin.txt"
(cd "$REPO" && go run -tags clockcheck ./android/tools/clockcheck "$SCENARIOS") > "$OUT/go.txt"
lines="$(wc -l < "$OUT/go.txt")"
scenarios="$(grep -c '^scenario ' "$SCENARIOS")"
if cmp -s "$OUT/go.txt" "$OUT/kotlin.txt"; then
    echo "PASS: $scenarios scenarios, $lines state lines identical (Go SideClock vs SideClock.kt)"
    [[ -z "${CLOCKCHECK_KEEP:-}" ]] || cp "$OUT/go.txt" "$CLOCKCHECK_KEEP"
else
    echo "FAIL: Go and Kotlin SideClock differ" >&2
    diff "$OUT/go.txt" "$OUT/kotlin.txt" | head -20 >&2
    exit 1
fi
