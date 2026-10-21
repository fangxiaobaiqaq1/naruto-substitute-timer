#!/bin/bash
# Runs tests/perf_kernels_test under QEMU TCG with an AVX2 CPU model, so the AVX2
# NCC kernel is executed even on hosts without AVX2 (docs/性能优化-android-native.md).
# Needs qemu-system-x86_64 and an x86_64 kernel bzImage (default: the Android SDK
# emulator's kernel-ranchu). Usage: tests/perf_kernels_qemu.sh [kernel] [cpu-model]
set -euo pipefail
CPP="$(cd "$(dirname "$0")/.." && pwd)"
REPO="$(cd "$CPP/../../../../.." && pwd)"
KERNEL="${1:-$(ls "${ANDROID_HOME:-$HOME/Android/Sdk}"/system-images/*/*/x86_64/kernel-ranchu 2>/dev/null | head -1)}"
CPU="${2:-Haswell}"
[[ -f "$KERNEL" ]] || { echo "need an x86_64 kernel bzImage (arg 1)" >&2; exit 2; }
OUT="$(mktemp -d "${TMPDIR:-/tmp}/nt-perf-qemu.XXXXXX")"
trap 'rm -rf "$OUT"' EXIT
HOSTCHECK_OUT="$OUT/hc" bash "$REPO/android/tools/hostcheck.sh"
"${CXX:-g++}" -std=c++17 -O2 -fno-fast-math -ffp-contract=off -pthread -static -I "$CPP/include" \
    "$CPP/tests/perf_kernels_test.cpp" "$OUT/hc/libnarutocore_host.a" -o "$OUT/test"
# /init: run /test with stdout/stderr on /dev/kmsg (shown by the early serial console), then power off.
cat > "$OUT/init.c" <<'EOF'
#include <fcntl.h>
#include <stdio.h>
#include <sys/reboot.h>
#include <sys/wait.h>
#include <unistd.h>
int main(void) {
    int fd = open("/dev/kmsg", O_WRONLY);
    if (fd >= 0) { dup2(fd, 1); dup2(fd, 2); }
    pid_t p = fork();
    if (p == 0) { char* args[] = {"/test", NULL}; execv("/test", args); _exit(127); }
    int st = 0;
    waitpid(p, &st, 0);
    dprintf(1, "TEST-EXIT status=%d\n", WIFEXITED(st) ? WEXITSTATUS(st) : -1);
    sync();
    reboot(RB_POWER_OFF);
    return 0;
}
EOF
"${CC:-gcc}" -O2 -static "$OUT/init.c" -o "$OUT/init"
python3 - "$OUT/init" "$OUT/test" "$OUT/initrd.cpio" <<'EOF'
import stat, sys
ino = [1]
def entry(name, mode, data=b'', major=0, minor=0):
    ino[0] += 1
    namez = name.encode() + b'\0'
    hdr = '070701' + ''.join('%08X' % v for v in [ino[0], mode, 0, 0, 1, 0, len(data), 0, 0, major, minor, len(namez), 0])
    out = hdr.encode() + namez
    out += b'\0' * ((4 - len(out) % 4) % 4) + data
    return out + b'\0' * ((4 - len(out) % 4) % 4)
blob = entry('dev', stat.S_IFDIR | 0o755)
blob += entry('dev/console', stat.S_IFCHR | 0o600, major=5, minor=1)
blob += entry('dev/kmsg', stat.S_IFCHR | 0o600, major=1, minor=11)
blob += entry('init', stat.S_IFREG | 0o755, open(sys.argv[1], 'rb').read())
blob += entry('test', stat.S_IFREG | 0o755, open(sys.argv[2], 'rb').read())
blob += entry('TRAILER!!!', 0)
open(sys.argv[3], 'wb').write(blob + b'\0' * ((512 - len(blob) % 512) % 512))
EOF
timeout 1800 qemu-system-x86_64 -accel tcg -cpu "$CPU" -smp 2 -m 1024 -kernel "$KERNEL" -initrd "$OUT/initrd.cpio" \
    -nographic -no-reboot -append "earlyprintk=serial keep_bootcon panic=-1" > "$OUT/boot.log" 2>&1 || true
grep -a -E "NCC kernels|perf_kernels_test|FAIL|TEST-EXIT" "$OUT/boot.log" | sed 's/^\[[ 0-9.]*\] //'
grep -a -q "TEST-EXIT status=0" "$OUT/boot.log"
