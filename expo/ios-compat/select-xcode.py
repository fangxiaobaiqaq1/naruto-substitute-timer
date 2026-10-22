#!/usr/bin/env python3
"""Use the newest installed stable Xcode, never a preview or an absent hardcoded path."""
import os
import pathlib
import re
import subprocess

choices = []
for app in pathlib.Path('/Applications').glob('Xcode*.app'):
    if app.is_symlink() or re.search(r'beta|preview|rc', app.name, re.I):
        continue
    developer = app / 'Contents/Developer'
    result = subprocess.run([str(developer / 'usr/bin/xcodebuild'), '-version'], capture_output=True, text=True)
    match = re.search(r'^Xcode (\d+(?:\.\d+){0,2})\s*$', result.stdout, re.M)
    build = re.search(r'Build version (\S+)', result.stdout)
    # Apple prerelease build numbers end with a lowercase suffix (17A123a).
    if result.returncode or not match or not build or re.search(r'[a-z]$', build[1]):
        continue
    version = tuple(map(int, match[1].split('.')))
    choices.append((version + (0,) * (3 - len(version)), str(developer), result.stdout.strip()))
if not choices:
    raise SystemExit('No installed stable Xcode found; refusing a beta build.')
version, developer, description = max(choices)
assert version >= (26, 6, 0), f'Runner Xcode too old: {version}'
with open(os.environ['GITHUB_ENV'], 'a', encoding='utf-8') as output:
    output.write(f'DEVELOPER_DIR={developer}\n')
print(f'Selected {developer}\n{description}')
