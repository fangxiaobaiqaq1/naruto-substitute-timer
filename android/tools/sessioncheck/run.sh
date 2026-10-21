#!/usr/bin/env bash
# Session parity: replays scenarios.seq through the production Android Session/SideClock/NinjaRules
# (host JVM, chaincheck Context stubs) and through Go internal/ui session.captureOnce (go test -overlay;
# the Go tree is not modified), then diffs the canonical per-report lines. Exit 0 only if all are equal.
# Run: android/tools/sessioncheck/run.sh [scenarios.seq]      Keep outputs: OUT=/tmp/dir .../run.sh
# Needs JDK 17, android-35 android.jar, Kotlin 2.0.21 jars in the Gradle cache, Go (go.mod version).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
KT="$REPO/android/app/src/main/java/com/narutotimer"
STUBS="$REPO/android/tools/chaincheck/stubs"
SCENARIOS="$(realpath "${1:-$HERE/scenarios.seq}")"
OUT="${OUT:-$(mktemp -d /tmp/naruto-sessioncheck.XXXXXX)}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
JAVA_HOME="${JAVA_HOME:-$HOME/.local/opt/jdk-17.0.20.1+1}"
ANDROID_JAR="${ANDROID_JAR:-${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Android/Sdk}}/platforms/android-35/android.jar}"
CACHE="${GRADLE_USER_HOME:-$HOME/.gradle}/caches/modules-2/files-2.1"
GO="${GO:-$(command -v go || echo /usr/local/go/bin/go)}"
[[ -x "$JAVA_HOME/bin/java" && -f "$ANDROID_JAR" && -x "$GO" ]] || {
    echo "Need JDK17 (JAVA_HOME), android-35/android.jar (ANDROID_JAR) and go (GO)" >&2; exit 1;
}
jar() {
    local result
    result="$(find "$CACHE/$1/$2/$3" -name "$2-$3.jar" -print -quit 2>/dev/null)"
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
PRODUCTION=("$KT/NativeCore.kt" "$KT/Settings.kt" "$KT/tracker/Session.kt" "$KT/tracker/SideClock.kt" "$KT/tracker/NinjaRules.kt")
[[ -f "$KT/capture/CaptureMeta.kt" ]] && PRODUCTION+=("$KT/capture/CaptureMeta.kt")
# Settings.calibRect applies the calibration size limits (NC-01).
[[ -f "$KT/CalibrationLimits.kt" ]] && PRODUCTION+=("$KT/CalibrationLimits.kt")
# Session's observation gap (Go app.ObservationCadence, GE-02).
[[ -f "$KT/tracker/ObservationCadence.kt" ]] && PRODUCTION+=("$KT/tracker/ObservationCadence.kt")
rm -rf "$OUT/stubs" "$OUT/classes" "$OUT/tests"
mkdir -p "$OUT/stubs" "$OUT/classes" "$OUT/tests"
"$JAVA_HOME/bin/javac" --release 17 -d "$OUT/stubs" "$STUBS/android/content/Context.java" \
    "$STUBS/android/content/SharedPreferences.java" "$STUBS/android/hardware/HardwareBuffer.java"
kotlinc() {
    "$JAVA_HOME/bin/java" -cp "$COMPILER" org.jetbrains.kotlin.cli.jvm.K2JVMCompiler \
        -jvm-target 17 -no-stdlib -no-reflect -nowarn "$@"
}
# Production compiles against the real Android API; only the driver sees the host Context.
kotlinc -classpath "$STDLIB:$ANNOTATIONS:$ANDROID_JAR" -d "$OUT/classes" "${PRODUCTION[@]}"
kotlinc -classpath "$OUT/classes:$OUT/stubs:$STDLIB:$ANNOTATIONS" -d "$OUT/tests" "$HERE/SessionCheck.kt"
# Both declaration forms: `inline const std::string MethodX = "...";` and (since NC-06, never
# destroyed) `inline const std::string& MethodX = *new std::string("...");`.
NATIVE_METHODS="$(sed -n -E 's/^inline const std::string&? Method[A-Za-z]* = (\*new std::string\()?"([^"]*)"\)?;$/\2/p' \
    "$REPO/android/app/src/main/cpp/include/nt/frame.h" | paste -sd, -)"
[[ -n "$NATIVE_METHODS" ]] || { echo "no Method* constants found in nt/frame.h" >&2; exit 1; }
"$JAVA_HOME/bin/java" -Dfile.encoding=UTF-8 -cp "$OUT/tests:$OUT/classes:$OUT/stubs:$STDLIB" \
    sessioncheck.SessionCheckKt "$SCENARIOS" "$NATIVE_METHODS" > "$OUT/android.txt"
printf '{"Replace":{"%s":"%s"}}\n' "$REPO/internal/ui/zz_sessioncheck_test.go" "$HERE/gosession.go" > "$OUT/overlay.json"
(cd "$REPO" && SESSIONCHECK_SCENARIOS="$SCENARIOS" SESSIONCHECK_OUT="$OUT/go.txt" "$GO" test -tags sessioncheck \
    -overlay "$OUT/overlay.json" -count=1 -run '^TestSessionCheck$' ./internal/ui > "$OUT/go-test.log" 2>&1) || {
    cat "$OUT/go-test.log" >&2; exit 1;
}
same=0; diff=0
while IFS= read -r g <&3 && IFS= read -r a <&4; do
    if [[ "$g" == "$a" ]]; then
        echo "SAME $g"; same=$((same + 1))
    else
        printf 'DIFF go:      %s\n     android: %s\n' "$g" "$a"; diff=$((diff + 1))
    fi
done 3< "$OUT/go.txt" 4< "$OUT/android.txt"
[[ "$(wc -l < "$OUT/go.txt")" == "$(wc -l < "$OUT/android.txt")" ]] || { echo "line counts differ" >&2; diff=$((diff + 1)); }
echo "sessioncheck: $same same, $diff different (outputs: $OUT)"
[[ $diff == 0 ]]
