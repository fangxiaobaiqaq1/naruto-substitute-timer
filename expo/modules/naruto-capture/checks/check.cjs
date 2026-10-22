'use strict';
// Run with `node modules/naruto-capture/checks/check.cjs` from expo/.
// Uses the installed xcode parser; no extra dependencies or Apple SDK required.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const root = path.resolve(__dirname, '../../..');
const { stageSources, configureProject } = require(path.join(root, 'plugins/withReplayKit'))._test;
const xcode = require(path.join(root, 'node_modules/xcode'));
const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'naruto-plugin-check-'));
const fixture = path.join(root, 'node_modules/react-native-safe-area-context/ios/RNSafeAreaContext.xcodeproj/project.pbxproj');
const unquote = (s) => String(s).replace(/^"|"$/g, '');
try {
  const project = xcode.project(fixture).parseSync();
  // Regression: Expo's app-only template has no dependency/proxy sections;
  // node-xcode addTarget silently skips installing dependencies in that case.
  delete project.hash.project.objects.PBXTargetDependency;
  delete project.hash.project.objects.PBXContainerItemProxy;
  for (const target of Object.values(project.pbxNativeTargetSection())) {
    if (target && typeof target === 'object') target.dependencies = [];
  }
  const sources = stageSources(root, temp, 'com.test.naruto.broadcast', '0.3.0', '1');
  assert(sources.some((s) => s.endsWith('RecognitionBridge.mm')));
  assert(sources.some((s) => s.endsWith('TimerSession.cpp')));
  assert(sources.some((s) => s.endsWith('Core/src/ninja/rules.cpp')));
  assert(!sources.some((s) => s.endsWith('jni.cpp')));
  const targetID = configureProject(project, sources, 'com.test.naruto.broadcast', null, '0.3.0', '1');
  const first = project.writeSync();
  configureProject(project, sources, 'com.test.naruto.broadcast', null, '0.3.0', '1');
  assert.equal(project.writeSync(), first, 'repeat prebuild must not duplicate native project entries');
  const objects = project.hash.project.objects;
  const target = objects.PBXNativeTarget[targetID];
  assert.equal(project.getFirstTarget().firstTarget.dependencies.filter(({ value }) => objects.PBXTargetDependency[value]?.target === targetID).length, 1, 'app must depend on the extension even in a fresh Expo project');
  const configs = objects.XCConfigurationList[target.buildConfigurationList].buildConfigurations;
  for (const config of configs) {
    const settings = objects.XCBuildConfiguration[config.value].buildSettings;
    assert.equal(settings.APPLICATION_EXTENSION_API_ONLY, 'YES');
    assert.equal(settings.SKIP_INSTALL, 'YES');
    assert.equal(unquote(settings.CLANG_CXX_LANGUAGE_STANDARD), 'c++17');
    assert.equal(settings.IPHONEOS_DEPLOYMENT_TARGET, '16.4');
    assert.equal(settings.SWIFT_VERSION, '5.0');
    assert(!settings.CODE_SIGN_ENTITLEMENTS);
  }
  const embeds = project.getFirstTarget().firstTarget.buildPhases
    .map(({ value }) => objects.PBXCopyFilesBuildPhase?.[value]).filter(Boolean);
  assert.equal(embeds.filter((phase) => phase.dstSubfolderSpec == 13 && phase.files.some(({ value }) => objects.PBXBuildFile[value].fileRef === target.productReference)).length, 1);
  const hash = (file) => crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
  const repo = path.dirname(root);
  const avatar = fs.readdirSync(path.join(repo, 'assets/avatars')).find((name) => name.endsWith('.png'));
  assert.equal(hash(path.join(temp, 'NarutoBroadcast/RecognitionAssets/avatars', avatar)), hash(path.join(repo, 'assets/avatars', avatar)));
  assert(!fs.existsSync(path.join(temp, 'NarutoBroadcast/RecognitionAssets/ocr/recognizer.onnx')), 'iOS uses native Vision, not the heavyweight ONNX model');
  assert(sources.some((s) => s.endsWith('VisionAccounts.mm')));
  const info = require(path.join(root, 'node_modules/@expo/plist')).default.parse(fs.readFileSync(path.join(temp, 'NarutoBroadcast/NarutoBroadcast-Info.plist'), 'utf8'));
  assert.equal(info.NSExtension.NSExtensionPointIdentifier, 'com.apple.broadcast-services-upload');
  assert.equal(info.NSExtension.NSExtensionAttributes.RPBroadcastProcessMode, 'RPBroadcastProcessModeSampleBuffer');
  // Round-trip parser guards malformed quoting/project structure.
  const out = path.join(temp, 'project.pbxproj');
  fs.writeFileSync(out, project.writeSync());
  xcode.project(out).parseSync();
  console.log(`PASS native plugin: ${sources.length} sources, idempotent target/embed, extension settings, assets/hash and pbxproj round-trip`);
  if (process.platform === 'darwin') {
    const { execFileSync } = require('node:child_process');
    const executable = path.join(temp, 'protocol-check');
    execFileSync('xcrun', ['swiftc', '-swift-version', '5',
      path.join(__dirname, '../ios/CaptureProtocol.swift'), path.join(__dirname, 'main.swift'), '-o', executable], { stdio: 'inherit' });
    execFileSync(executable, [], { stdio: 'inherit' });
  } else {
    console.log('SKIP Swift protocol compilation: requires the macOS CI toolchain');
  }
} finally { fs.rmSync(temp, { recursive: true, force: true }); }
