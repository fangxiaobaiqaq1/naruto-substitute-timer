#!/usr/bin/env bash
# CalibrationScheme (方案文件, docs/区域校准.md) against the REAL org.json of a device:
# compiles the production ui/CalibrationScheme.kt + SchemeCheck.kt, dexes them with d8
# and runs them with app_process on the connected device/emulator (adb). The host JVM
# only has android.jar's org.json stubs, hence the device. No APK install, no Gradle.
# Run: android/tools/schemecheck/run.sh   (ANDROID_SERIAL picks a device)
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
KT="$REPO/android/app/src/main/java/com/narutotimer"
JAVA_HOME="${JAVA_HOME:-$HOME/.local/opt/jdk-17.0.20.1+1}"
SDK="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Android/Sdk}}"
ANDROID_JAR="${ANDROID_JAR:-$SDK/platforms/android-35/android.jar}"
D8="$(ls -d "$SDK"/build-tools/*/d8 2>/dev/null | sort -V | tail -n 1)"
ADB="${ADB:-$SDK/platform-tools/adb}"
CACHE="${GRADLE_USER_HOME:-$HOME/.gradle}/caches/modules-2/files-2.1"
[[ -x "$JAVA_HOME/bin/java" && -f "$ANDROID_JAR" && -n "$D8" && -x "$ADB" ]] || {
    echo "Need JDK17 (JAVA_HOME), android-35/android.jar, build-tools d8 and adb" >&2; exit 1;
}
"$ADB" get-state >/dev/null 2>&1 || { echo "No device: start an emulator or connect one (adb devices)" >&2; exit 1; }
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
OUT="$(mktemp -d /tmp/naruto-schemecheck.XXXXXX)"
trap 'rm -rf "$OUT"' EXIT
# Compiled against android.jar: the codec may use nothing beyond the platform API.
"$JAVA_HOME/bin/java" -cp "$COMPILER" org.jetbrains.kotlin.cli.jvm.K2JVMCompiler \
    -jvm-target 17 -no-stdlib -no-reflect -classpath "$STDLIB:$ANNOTATIONS:$ANDROID_JAR" \
    -d "$OUT/check.jar" "$KT/ui/CalibrationScheme.kt" "$HERE/SchemeCheck.kt"
"$D8" --min-api 31 --lib "$ANDROID_JAR" --output "$OUT/check-dex.jar" "$OUT/check.jar" "$STDLIB" 2>&1 \
    | grep -v -E '^Warning: (Missing class|Type `)' || true
REMOTE=/data/local/tmp/nt-schemecheck.jar
"$ADB" push "$OUT/check-dex.jar" "$REMOTE" >/dev/null
RESULT="$("$ADB" shell "CLASSPATH=$REMOTE app_process /system/bin schemecheck.SchemeCheckKt; echo exit=\$?")"
"$ADB" shell rm -f "$REMOTE" >/dev/null 2>&1 || true
printf '%s\n' "$RESULT"
grep -q '^PASS: ' <<<"$RESULT" && grep -q '^exit=0' <<<"$RESULT"
