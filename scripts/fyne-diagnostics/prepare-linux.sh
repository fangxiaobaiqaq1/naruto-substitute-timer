#!/usr/bin/env bash
# Generate the Fyne v2.6.3 diagnostics overlay on a Linux host.
# This is the Linux equivalent of prepare.ps1: it writes only the requested
# output directory and creates a symlink alias rather than modifying GOMODCACHE.
set -euo pipefail

output_directory=${1:-bin/fyne-diagnostics}
script_directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
project_directory=$(cd -- "$script_directory/../.." && pwd)
output_path=$(mkdir -p -- "$output_directory" && cd -- "$output_directory" && pwd)

cd -- "$project_directory"
module_version=$(go list -m -f '{{.Version}}' fyne.io/fyne/v2)
module_replace=$(go list -m -f '{{with .Replace}}{{.Path}}{{end}}' fyne.io/fyne/v2)
module_directory=$(go list -m -f '{{.Dir}}' fyne.io/fyne/v2)
if [[ $module_version != v2.6.3 || -n $module_replace || ! -d $module_directory ]]; then
    echo "The diagnostics renderer overlay supports unmodified fyne.io/fyne/v2 v2.6.3 only." >&2
    exit 1
fi

module_alias=$output_path/fyne
module_directory=$(cd -- "$module_directory" && pwd)
if [[ -e $module_alias || -L $module_alias ]]; then
    if [[ ! -L $module_alias || $(readlink -f -- "$module_alias") != "$module_directory" ]]; then
        echo "The existing Fyne alias is not the expected symlink: $module_alias" >&2
        exit 1
    fi
else
    ln -s -- "$module_directory" "$module_alias"
fi

python3 - "$module_alias" "$output_path/_source" "$script_directory/frame_hook.go.txt" <<'PY'
import pathlib
import sys

module_alias, source_path, hook_path = map(pathlib.Path, sys.argv[1:])


def replace_once(source, before, after, name):
    count = source.count(before)
    if count != 1:
        raise SystemExit(
            f"Fyne diagnostics patch {name!r} expected one anchor; found {count}. "
            "Review the toolkit changes before building."
        )
    return source.replace(before, after)


driver_path = module_alias / "internal" / "driver" / "glfw"
loop_path = driver_path / "loop.go"
window_path = driver_path / "window.go"
loop_source = loop_path.read_text().replace("\r\n", "\n")
window_source = window_path.read_text().replace("\r\n", "\n")

loop_source = replace_once(
    loop_source,
    "func (d *gLDriver) repaintWindow(w *window) bool {\n",
    "func (d *gLDriver) repaintWindow(w *window) bool {\n"
    "\t// Snapshot the revision-specific callback before any rendering begins.\n"
    "\tonFrameDraw := timerDiagnosticsFrameCallback(w)\n"
    "\tvar drawStarted time.Time\n"
    "\tif onFrameDraw != nil {\n\t\tdrawStarted = time.Now()\n\t}\n",
    "draw start",
)
loop_source = replace_once(
    loop_source,
    "\tif view != nil && visible {\n\t\tview.SwapBuffers()\n\t}",
    "\tif view != nil && visible {\n\t\tview.SwapBuffers()\n"
    "\t\tif onFrameDraw != nil {\n\t\t\tonFrameDraw(drawStarted, time.Now())\n\t\t}\n\t}",
    "visible buffer submission",
)
window_source = replace_once(
    window_source,
    "func (w *window) destroy(d *gLDriver) {\n",
    "func (w *window) destroy(d *gLDriver) {\n\ttimerDiagnosticsFrameCallbacks.Delete(w)\n",
    "window callback cleanup",
)

source_path.mkdir(parents=True, exist_ok=True)
(source_path / "loop.go").write_text(loop_source)
(source_path / "window.go").write_text(window_source)
(source_path / "timer_diagnostics_frame.go").write_text(hook_path.read_text())
PY

gofmt -w "$output_path/_source/loop.go" "$output_path/_source/window.go" "$output_path/_source/timer_diagnostics_frame.go"
cp go.mod "$output_path/diagnostics.mod"
cp go.sum "$output_path/diagnostics.sum"
go mod edit -modfile="$output_path/diagnostics.mod" "-replace=fyne.io/fyne/v2=$module_alias"

python3 - "$module_alias" "$output_path/_source" "$output_path/overlay.json" <<'PY'
import json
import pathlib
import sys

module_alias, source_path, overlay_path = map(pathlib.Path, sys.argv[1:])
driver_path = module_alias / "internal" / "driver" / "glfw"
overlay = {
    "Replace": {
        str(driver_path / "loop.go"): str(source_path / "loop.go"),
        str(driver_path / "window.go"): str(source_path / "window.go"),
        str(driver_path / "timer_diagnostics_frame.go"): str(source_path / "timer_diagnostics_frame.go"),
    }
}
overlay_path.write_text(json.dumps(overlay, indent=2) + "\n")
PY

printf '%s\n' "$output_path/overlay.json"
