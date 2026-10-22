// CI/prebuild integration gate: a foreground-only app must never pass as the IPA product.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const xcode = require('xcode');
const plist = require('@expo/plist').default;
const ios = path.resolve(__dirname, '../ios');
const projectDirs = fs.readdirSync(ios).filter((name) => name.endsWith('.xcodeproj'));
assert.equal(projectDirs.length, 1, 'exactly one generated Xcode project');
const project = xcode.project(path.join(ios, projectDirs[0], 'project.pbxproj')).parseSync();
const objects = project.hash.project.objects;
const unquote = (value) => String(value || '').replace(/^"|"$/g, '');
const targets = Object.entries(objects.PBXNativeTarget).filter(([id]) => !id.endsWith('_comment'));
const apps = targets.filter(([, target]) => unquote(target.productType) === 'com.apple.product-type.application');
const extensions = targets.filter(([, target]) => unquote(target.productType) === 'com.apple.product-type.app-extension');
assert.equal(apps.length, 1); assert.equal(extensions.length, 1, 'missing or duplicate Broadcast Upload Extension');
const [extensionID, extension] = extensions[0];
const app = apps[0][1];
assert.ok(app.dependencies.some(({ value }) => objects.PBXTargetDependency[value]?.target === extensionID), 'app must build the extension');
assert.ok(app.buildPhases.some(({ value }) => {
  const phase = objects.PBXCopyFilesBuildPhase?.[value];
  return Number(phase?.dstSubfolderSpec) === 13 && phase.files.some(({ value }) => objects.PBXBuildFile[value].fileRef === extension.productReference);
}), 'app must embed the extension into PlugIns');
const configs = objects.XCConfigurationList[extension.buildConfigurationList].buildConfigurations;
for (const { value } of configs) {
  const settings = objects.XCBuildConfiguration[value].buildSettings;
  assert.equal(unquote(settings.APPLICATION_EXTENSION_API_ONLY), 'YES');
  assert.equal(unquote(settings.SKIP_INSTALL), 'YES');
  const info = plist.parse(fs.readFileSync(path.join(ios, unquote(settings.INFOPLIST_FILE)), 'utf8'));
  assert.equal(info.NSExtension.NSExtensionPointIdentifier, 'com.apple.broadcast-services-upload');
  assert.equal(info.NSExtension.NSExtensionAttributes.RPBroadcastProcessMode, 'RPBroadcastProcessModeSampleBuffer');
  assert.ok(info.NSExtension.NSExtensionPrincipalClass.endsWith('.SampleHandler'));
}
const sourcePhase = extension.buildPhases.map(({ value }) => objects.PBXSourcesBuildPhase?.[value]).find(Boolean);
const sourcePaths = sourcePhase.files.map(({ value }) => unquote(objects.PBXFileReference[objects.PBXBuildFile[value].fileRef].path));
for (const name of ['SampleHandler.swift', 'RecognitionBridge.mm', 'TimerSession.cpp', 'configured_layout.cpp', 'gated.cpp', 'avatar.cpp', 'rules.cpp']) {
  assert.ok(sourcePaths.some((file) => file.endsWith('/' + name)), `${name} must be compiled into the extension`);
}
assert.equal(new Set(sourcePaths).size, sourcePaths.length, 'prebuild must not duplicate source entries');
assert.ok(!sourcePaths.some((file) => file.endsWith('/jni.cpp')), 'Android/JNI is not an iOS source');
const resourcePhase = extension.buildPhases.map(({ value }) => objects.PBXResourcesBuildPhase?.[value]).find(Boolean);
const assetPath = resourcePhase.files.map(({ value }) => unquote(objects.PBXFileReference[objects.PBXBuildFile[value].fileRef].path)).find((file) => file.endsWith('/RecognitionAssets'));
assert.ok(assetPath, 'extension recognition resource folder');
for (const file of ['templates/manifest.json', 'avatars/index.json']) assert.ok(fs.statSync(path.join(ios, assetPath, file)).size > 0, file);
console.log(`iOS project passed: 1 app + 1 embedded broadcast, ${sourcePaths.length} native recognition sources, original assets.`);
