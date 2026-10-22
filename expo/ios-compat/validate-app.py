#!/usr/bin/env python3
"""Validate the actual unsigned device bundle, not merely the Xcode project."""
import hashlib
import pathlib
import plistlib
import subprocess
import sys

app = pathlib.Path(sys.argv[1])
root = pathlib.Path(__file__).resolve().parents[2]
info = plistlib.loads((app / 'Info.plist').read_bytes())
assert info['CFBundleSupportedPlatforms'] == ['iPhoneOS']
assert 'audio' in info.get('UIBackgroundModes', []), 'PiP background mode missing'
assert info.get('NRTReplayKitBroadcastExtensionConfigured') is True
assert 'NRTReplayKitBridgeStatus' not in info, 'placeholder-only app'
assert (app / 'main.jsbundle').stat().st_size > 0
extensions = list((app / 'PlugIns').glob('*.appex'))
assert len(extensions) == 1, 'missing or duplicate broadcast extension'
extension = extensions[0]
ext = plistlib.loads((extension / 'Info.plist').read_bytes())
assert ext['CFBundleIdentifier'] == info['NRTBroadcastExtensionIdentifier']
assert ext['CFBundleIdentifier'].startswith(info['CFBundleIdentifier'] + '.')
assert ext['CFBundleShortVersionString'] == info['CFBundleShortVersionString']
assert ext['CFBundleVersion'] == info['CFBundleVersion']
assert ext['NSExtension']['NSExtensionPointIdentifier'] == 'com.apple.broadcast-services-upload'
assert ext['NSExtension']['NSExtensionAttributes']['RPBroadcastProcessMode'] == 'RPBroadcastProcessModeSampleBuffer'
for bundle, metadata in [(app, info), (extension, ext)]:
    executable = bundle / metadata['CFBundleExecutable']
    assert executable.stat().st_size > 0
    subprocess.run(['xcrun', 'lipo', str(executable), '-verify_arch', 'arm64'], check=True)
assets = extension / 'RecognitionAssets'
for relative in ['templates/manifest.json', 'avatars/index.json', 'game']:
    assert (assets / relative).exists(), f'missing recognition resource: {relative}'
# Avatar/core manifests hash encoded PNGs. Xcode PNG rewriting breaks recognition.
checked = 0
for directory in ['templates', 'avatars']:
    for original in (root / 'assets' / directory).rglob('*.png'):
        if original.name.endswith('.source.png'):
            continue
        packed = assets / directory / original.relative_to(root / 'assets' / directory)
        assert packed.exists(), f'missing original asset: {packed}'
        assert hashlib.sha256(original.read_bytes()).digest() == hashlib.sha256(packed.read_bytes()).digest(), f're-encoded asset: {packed}'
        checked += 1
assert checked > 0
print(f'Validated {info["CFBundleShortVersionString"]}: device arm64 app + broadcast extension, JS bundle and {checked} byte-identical PNGs.')
