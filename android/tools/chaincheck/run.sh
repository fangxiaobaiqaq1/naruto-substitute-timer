#!/usr/bin/env bash
# Controlled pixels -> production JNI/Decoder -> Settings/Session/SideClock.
# NOT recorded gameplay, Android OS capture, HardwareBuffer, or Canvas validation.
# Run: android/tools/chaincheck/run.sh [ordinary.png] [special.png] [exported-matrix-dir]
# Export the optional matrix with NARUTO_MATRIX_OUT=/tmp/matrix go test ./internal/ui \
#   -run '^TestSpecialHUDResolutionMatrix$' -count=1
# Reuse this check's *own* output: ANDROID_CHAIN_OUT=/tmp/my-chaincheck .../run.sh
# No Gradle/network/APK build; Kotlin 2.0.21 and dependencies come from Gradle cache.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
CPP="$REPO/android/app/src/main/cpp"
KT="$REPO/android/app/src/main/java/com/narutotimer"
OUT="${ANDROID_CHAIN_OUT:-$(mktemp -d /tmp/naruto-android-chain.XXXXXX)}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
exec 9>"$OUT/.lock"
flock -n 9 || { echo "Another chaincheck owns $OUT" >&2; exit 1; }
echo "Independent host output: $OUT"
JAVA_HOME="${JAVA_HOME:-$HOME/.local/opt/jdk-17.0.20.1+1}"
ANDROID_JAR="${ANDROID_JAR:-${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Android/Sdk}}/platforms/android-35/android.jar}"
CACHE="${GRADLE_USER_HOME:-$HOME/.gradle}/caches/modules-2/files-2.1"
FIXTURE="${1:-$REPO/inbox/regressions/wide-overlay-beans-20261009.png}"
SPECIAL_FIXTURE="${2:-$REPO/inbox/regressions/xiayin-wide-overlay-20261009.png}"
MATRIX="${3:-${NARUTO_MATRIX_OUT:-}}"
[[ -z "$MATRIX" || -d "$MATRIX" ]] || { echo "Missing exported matrix directory: $MATRIX" >&2; exit 1; }
for fixture in "$FIXTURE" "$SPECIAL_FIXTURE"; do
    [[ -f "$fixture" ]] || { echo "Missing required user PNG fixture: $fixture" >&2; exit 1; }
done
[[ -x "$JAVA_HOME/bin/java" && -f "$ANDROID_JAR" ]] || {
    echo "Need JDK17 (JAVA_HOME) and android-35/android.jar (ANDROID_JAR)" >&2; exit 1;
}
grep -q 'Catalog::StartFight(' "$CPP/src/scene/scene.cpp" &&
    grep -q 'start->StartFight(' "$CPP/src/engine/gated.cpp" || {
    echo "C++ mirror's StartFight implementation/call is not ready; retry after it lands" >&2; exit 1;
}
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
PRODUCTION=("$KT/NativeCore.kt" "$KT/Settings.kt" "$KT/CalibrationLimits.kt" "$KT/tracker/Session.kt" "$KT/tracker/SideClock.kt" "$KT/tracker/ObservationCadence.kt" "$KT/tracker/NinjaRules.kt")
# Refuse to report success from a build raced by a concurrently edited mirror.
snapshot() {
    {
        find "$CPP/src" "$CPP/include" -type f \( -name '*.cpp' -o -name '*.h' \) -print0
        find "$REPO/assets/templates" "$REPO/assets/avatars" "$REPO/assets/game" \
            "$REPO/internal/ninja/templates" -type f \( -name '*.json' -o -name '*.png' \) -print0
        if [[ -n "$MATRIX" ]]; then find "$MATRIX" -type f \( -name '*.png' -o -name 'case.tsv' \) -print0; fi
        printf '%s\0' "$CPP/jni.cpp" "${PRODUCTION[@]}" "$FIXTURE" "$SPECIAL_FIXTURE" \
            "$HERE/ChainCheck.kt" "$HERE/run.sh"
    } | sort -z | xargs -0 sha256sum
}
snapshot > "$OUT/production.before.sha256"
# Never reuse the mirror agent's android/build/hostcheck (non-PIC) objects.
HOSTCHECK_OUT="$OUT/pic" EXTRA_CXXFLAGS=-fPIC EXTRA_LDFLAGS= \
    bash "$HERE/../hostcheck.sh"
"${CXX:-g++}" -std=c++17 -O2 -g -fPIC -shared -fno-fast-math -ffp-contract=off -pthread \
    -Wall -Wno-unused-function -I "$HERE/stubs" -I "$CPP/include" \
    -I "$JAVA_HOME/include" -I "$JAVA_HOME/include/linux" \
    "$CPP/jni.cpp" "$HERE/host_pixels.cpp" "$OUT/pic/libnarutocore_host.a" \
    -Wl,-z,defs -o "$OUT/libnarutocore.so"
mkdir -p "$OUT/stubs" "$OUT/classes" "$OUT/tests"
"$JAVA_HOME/bin/javac" --release 17 -d "$OUT/stubs" \
    "$HERE/stubs/android/content/Context.java" "$HERE/stubs/android/content/SharedPreferences.java" \
    "$HERE/stubs/android/hardware/HardwareBuffer.java"
kotlinc() {
    "$JAVA_HOME/bin/java" -cp "$COMPILER" org.jetbrains.kotlin.cli.jvm.K2JVMCompiler \
        -jvm-target 17 -no-stdlib -no-reflect "$@"
}
# Compile production against the real Android SDK API, not the in-memory shells.
kotlinc -classpath "$STDLIB:$ANNOTATIONS:$ANDROID_JAR" -d "$OUT/classes" "${PRODUCTION[@]}"
# Only the test driver is compiled against the instantiable host Context.
kotlinc -classpath "$OUT/classes:$OUT/stubs:$STDLIB:$ANNOTATIONS" \
    -d "$OUT/tests" "$HERE/ChainCheck.kt"
"$JAVA_HOME/bin/java" -Dfile.encoding=UTF-8 -Djava.awt.headless=true -XX:MaxDirectMemorySize=512m -Djava.library.path="$OUT" \
    -cp "$OUT/tests:$OUT/classes:$OUT/stubs:$STDLIB" hostchain.ChainCheckKt "$REPO" "$FIXTURE" "$SPECIAL_FIXTURE" "$MATRIX" \
    | tee "$OUT/results.log"
snapshot > "$OUT/production.after.sha256"
cmp -s "$OUT/production.before.sha256" "$OUT/production.after.sha256" || {
    echo "Production inputs changed during this run; results are provisional. Rerun." >&2; exit 1;
}
echo "PASS: stable production inputs; trace: $OUT/results.log"
