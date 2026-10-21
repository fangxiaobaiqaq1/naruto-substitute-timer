#!/usr/bin/env bash
# CalibrationLimits.kt (docs/区域校准.md 框的大小限制) on the host JVM: the same decisions as Go and
# the C++ core (LimitsCheck.kt), and Settings.kt with boxes stored by versions without the limits.
# No device, no Gradle; Kotlin 2.0.21 from the Gradle cache, the chaincheck Context stubs.
# Run: android/tools/limitscheck/run.sh
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
KT="$REPO/android/app/src/main/java/com/narutotimer"
JAVA_HOME="${JAVA_HOME:-$HOME/.local/opt/jdk-17.0.20.1+1}"
CACHE="${GRADLE_USER_HOME:-$HOME/.gradle}/caches/modules-2/files-2.1"
[[ -x "$JAVA_HOME/bin/java" ]] || { echo "Need JDK17 (JAVA_HOME)" >&2; exit 1; }
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
OUT="$(mktemp -d /tmp/naruto-limitscheck.XXXXXX)"
trap 'rm -rf "$OUT"' EXIT
# Settings.kt (stored boxes) runs on the chaincheck in-memory Context/SharedPreferences stubs.
STUBS="$REPO/android/tools/chaincheck/stubs/android/content"
mkdir -p "$OUT/stubs"
"$JAVA_HOME/bin/javac" --release 17 -d "$OUT/stubs" "$STUBS/Context.java" "$STUBS/SharedPreferences.java"
"$JAVA_HOME/bin/java" -cp "$COMPILER" org.jetbrains.kotlin.cli.jvm.K2JVMCompiler \
    -jvm-target 17 -no-stdlib -no-reflect -classpath "$STDLIB:$ANNOTATIONS:$OUT/stubs" \
    -d "$OUT/check.jar" "$KT/CalibrationLimits.kt" "$KT/Settings.kt" "$HERE/LimitsCheck.kt"
"$JAVA_HOME/bin/java" -Dfile.encoding=UTF-8 -cp "$OUT/check.jar:$OUT/stubs:$STDLIB" limitscheck.LimitsCheckKt
