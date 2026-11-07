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
if any("test-token" in arg for arg in args):
    raise SystemExit("token must not appear in curl argv")
header_files = [args[i + 1][1:] for i, arg in enumerate(args[:-1])
                if arg == "--header" and args[i + 1].startswith("@")]
if not any("Authorization: token test-token" in Path(f).read_text() for f in header_files):
    raise SystemExit("token must be passed in a curl header file")
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
def last_option(name):
    return [args[i + 1] for i, arg in enumerate(args[:-1]) if arg == name][-1]

if form is not None:
    method = "POST"
    assert "--ipv4" in args and "--http1.1" in args and "Expect:" in args
    assert last_option("--max-time") == "900"
    assert last_option("--retry") == "1"
    assert last_option("--speed-limit") == "1024" and last_option("--speed-time") == "60"
    assert last_option("--write-out").startswith("%{stderr}Gitee upload:")
else:
    assert last_option("--max-time") == "120"

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

# Run the mirror's actual verification block against tiny local fixtures.
python3 - "$script_dir/../../.github/workflows/mirror-gitee.yml" "$tmpdir" <<'PY'
import hashlib
import json
import os
import subprocess
import sys
import textwrap
from pathlib import Path

workflow = Path(sys.argv[1]).read_text()
verify = workflow.split("      - name: Verify GitHub digests and checksum manifest\n", 1)[1]
verify = textwrap.dedent(verify.split("        run: |\n", 1)[1].split("      - name: Publish Gitee release\n", 1)[0])
root = Path(sys.argv[2])
assets = root / "mirror-gitee/assets"
assets.mkdir(parents=True)
names = ["timer-app.exe", "naruto-timer-universal.apk", "naruto-timer-v0.3.1-windows-x64.zip"]
for name in names:
    (assets / name).write_text("fixture " + name)
def digest(name):
    return hashlib.sha256((assets / name).read_bytes()).hexdigest()
manifest = "".join(f"{digest(name)}  {name}\n" for name in names)
for content, good in [(manifest, True), (manifest.replace("timer-app.exe", "../timer-app.exe"), False),
                      (manifest + manifest.splitlines()[0] + "\n", False),
                      (manifest.replace(digest(names[0]), "0" * 64), False)]:
    (assets / "SHA256SUMS.txt").write_text(content)
    metadata = {"assets": [{"name":n, "state":"uploaded", "digest":"sha256:" + digest(n)}
                           for n in names + ["SHA256SUMS.txt"]]}
    (root / "mirror-gitee/release.json").write_text(json.dumps(metadata))
    result = subprocess.run(["bash", "-c", verify], cwd=assets, text=True, capture_output=True,
                            env=dict(os.environ, TAG="v0.3.1", RUNNER_TEMP=str(root)))
    assert (result.returncode == 0) == good, result.stdout + result.stderr
PY

# Exercise both workflows' exact helpers without contacting Gitee or storing credentials.
python3 - "$script_dir/../../.github/workflows/release.yml" "$script_dir/../../.github/workflows/mirror-gitee.yml" <<'PY'
import os
import shlex
import subprocess
import sys
from pathlib import Path

for path in sys.argv[1:]:
    workflow = Path(path).read_text()
    line = next(line.strip().removesuffix("\\").strip() for line in workflow.splitlines()
                if "-c 'credential.helper=!" in line)
    helper = shlex.split(line)[1]
    env = dict(os.environ, GITEE_REPOSITORY="test-owner/repo", GITEE_TOKEN="git-test-token", GIT_TERMINAL_PROMPT="0")
    result = subprocess.run(["git", "-c", "credential.helper=", "-c", helper, "credential", "fill"],
                            input="protocol=https\nhost=gitee.com\n\n", text=True, capture_output=True, env=env, check=True)
    fields = dict(line.split("=", 1) for line in result.stdout.splitlines() if "=" in line)
    assert fields["username"] == "test-owner" and fields["password"] == "git-test-token"
    assert all("git-test-token" not in arg for arg in result.args)
    assert '"refs/tags/$TAG:refs/tags/$TAG"' in workflow
PY

echo 'publish-gitee.sh, mirror validation and workflow credential helper mock tests passed'
