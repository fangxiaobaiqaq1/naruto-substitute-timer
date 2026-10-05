#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script="$script_dir/publish-gitee.sh"
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
bin="$tmpdir/bin"
mkdir -p "$bin"
state="$tmpdir/state.json"
log="$tmpdir/curl.log"
export MOCK_GITEE_STATE="$state" MOCK_GITEE_LOG="$log"

cat > "$bin/curl" <<'PY'
#!/usr/bin/env python3
import json
import os
import re
import shutil
import sys
from pathlib import Path
from urllib.parse import urlparse

args = sys.argv[1:]
url = args[-1]
if "access_token=" in url:
    raise SystemExit("token must not be sent in the URL")
method = "GET"
output = None
form = None
for i, arg in enumerate(args):
    if arg == "--request":
        method = args[i + 1]
    elif arg == "--output":
        output = args[i + 1]
    elif arg == "--form":
        form = args[i + 1]
if "--data" in args and method == "GET":
    method = "POST"
if form is not None:
    method = "POST"

state_path = Path(os.environ["MOCK_GITEE_STATE"])
log_path = Path(os.environ["MOCK_GITEE_LOG"])
with log_path.open("a") as f:
    f.write(f"{method} {url}\n")
state = {"created": False, "attachments": [], "next_id": 1}
if state_path.exists():
    state = json.loads(state_path.read_text())
path = urlparse(url).path

def emit(value):
    data = value if isinstance(value, bytes) else value.encode()
    if output:
        Path(output).write_bytes(data)
    else:
        sys.stdout.buffer.write(data)

def save():
    state_path.write_text(json.dumps(state))

if path.endswith("/releases") and method == "GET":
    emit(json.dumps([{"id": 42, "tag_name": "v0.2.9"}] if state["created"] else []))
elif path.endswith("/releases") and method == "POST":
    state["created"] = True
    save()
    emit('{"id":42}')
elif re.search(r"/releases/42$", path) and method == "PATCH":
    state["patched"] = True
    save()
    emit("{}")
elif path.endswith("/attach_files") and method == "GET":
    emit(json.dumps([{"id": a["id"], "name": a["name"], "size": Path(a["path"]).stat().st_size}
                     for a in state["attachments"]]))
elif (match := re.search(r"/attach_files/(\d+)/download$", path)) and method == "GET":
    asset = next(a for a in state["attachments"] if a["id"] == int(match.group(1)))
    emit(Path(asset["path"]).read_bytes())
elif (match := re.search(r"/attach_files/(\d+)$", path)) and method == "DELETE":
    asset_id = int(match.group(1))
    state["attachments"] = [a for a in state["attachments"] if a["id"] != asset_id]
    save()
    emit("{}")
elif path.endswith("/attach_files") and method == "POST":
    source = Path(form.removeprefix("file=@"))
    name = source.name
    stored = state_path.parent / f"remote-{state['next_id']}-{name}"
    shutil.copyfile(source, stored)
    asset_id = state["next_id"]
    state["next_id"] += 1
    state["attachments"].append({"id": asset_id, "name": name, "path": str(stored)})
    save()
    emit(json.dumps({"id": asset_id}))
else:
    raise SystemExit(f"unexpected mock curl request: {method} {url}")
PY
chmod +x "$bin/curl"

notes="$tmpdir/notes.md"
exe="$tmpdir/timer-app.exe"
sums="$tmpdir/SHA256SUMS.txt"
zip="$tmpdir/optional.zip"
printf 'mock executable\n' > "$exe"
printf 'release notes\n' > "$notes"
printf '%s  timer-app.exe\n' "$(sha256sum "$exe" | awk '{print $1}')" > "$sums"
truncate -s 100000001 "$zip"

run_publish() {
  PATH="$bin:$PATH" GITEE_TOKEN=test-token GITEE_REPOSITORY=test/repo \
    "$script" v0.2.9 "$notes" "$exe" "$sums" "$zip"
}

first_output="$tmpdir/first.out"
run_publish >"$first_output" 2>&1
grep -q 'Skipping optional ZIP over Gitee 100 MB limit: optional.zip' "$first_output"
grep -q 'Uploading SHA256SUMS.txt' "$first_output"
! grep -q 'Uploading optional.zip' "$first_output"

printf '0000000000000000000000000000000000000000000000000000000000000000  timer-app.exe\n' > "$sums"
second_output="$tmpdir/second.out"
run_publish >"$second_output" 2>&1
grep -q 'Updated Gitee release notes: v0.2.9' "$second_output"
grep -q 'Replacing changed Gitee checksum attachment: SHA256SUMS.txt' "$second_output"
! grep -q 'Uploading timer-app.exe' "$second_output"
! grep -q 'access_token=' "$log"
python3 - "$state" "$sums" <<'PY'
import json
import sys
from pathlib import Path

state = json.loads(Path(sys.argv[1]).read_text())
expected = Path(sys.argv[2]).read_bytes()
checksums = [a for a in state["attachments"] if a["name"] == "SHA256SUMS.txt"]
assert len(checksums) == 1
assert Path(checksums[0]["path"]).read_bytes() == expected
assert state.get("patched") is True
PY

bad="$tmpdir/too-large.apk"
truncate -s 100000001 "$bad"
before=$(wc -l < "$log")
if PATH="$bin:$PATH" GITEE_TOKEN=test-token GITEE_REPOSITORY=test/repo \
    "$script" v0.2.9 "$notes" "$bad" >"$tmpdir/bad.out" 2>&1; then
  echo 'oversized required APK unexpectedly succeeded' >&2
  exit 1
fi
grep -q 'Required artifact exceeds Gitee 100 MB limit: too-large.apk' "$tmpdir/bad.out"
[[ "$before" == "$(wc -l < "$log")" ]]

echo 'publish-gitee.sh mock test passed'
