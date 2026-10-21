#!/usr/bin/env bash
# Host check of the Android app's pure-Kotlin capture / identity / update / loader logic:
# capture/CaptureSize.kt, Housekeeping.kt, IdentityStore.kt (+ tracker/NinjaRules.kt for GoText),
# update/ApkCheck.kt, NewestWins.kt
# (AppCheck.kt says what each case covers). Compiled with Kotlin 2.0.21 from the Gradle cache and
# run on the host JVM: no device, no Gradle, no network.
# Run: android/tools/appcheck/run.sh
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
KT="$REPO/android/app/src/main/java/com/narutotimer"
JAVA_HOME="${JAVA_HOME:-$HOME/.local/opt/jdk-17.0.20.1+1}"
CACHE="${GRADLE_USER_HOME:-$HOME/.gradle}/caches/modules-2/files-2.1"
[[ -x "$JAVA_HOME/bin/java" ]] || { echo "Need JDK17 (JAVA_HOME)" >&2; exit 1; }
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
OUT="$(mktemp -d /tmp/naruto-appcheck.XXXXXX)"
trap 'rm -rf "$OUT"' EXIT
# No android.jar on the classpath: these files must stay free of Android APIs.
"$JAVA_HOME/bin/java" -cp "$COMPILER" org.jetbrains.kotlin.cli.jvm.K2JVMCompiler \
    -jvm-target 17 -no-stdlib -no-reflect -classpath "$STDLIB:$ANNOTATIONS" -d "$OUT/check.jar" \
    "$KT/capture/CaptureSize.kt" "$KT/Housekeeping.kt" "$KT/IdentityStore.kt" "$KT/tracker/NinjaRules.kt" \
    "$KT/update/ApkCheck.kt" "$KT/NewestWins.kt" "$HERE/AppCheck.kt"
"$JAVA_HOME/bin/java" -cp "$OUT/check.jar:$STDLIB" appcheck.AppCheckKt
