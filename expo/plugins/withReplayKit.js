'use strict';
const fs = require('node:fs');
const path = require('node:path');
const { withInfoPlist, withXcodeProject, createRunOncePlugin } = require('expo/config-plugins');
const plist = require('@expo/plist');

const TARGET = 'NarutoBroadcast';
const unquote = (value) => String(value || '').replace(/^"|"$/g, '');
const quote = (value) => JSON.stringify(value);

function filesBelow(root) {
  return fs.readdirSync(root, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name)).flatMap((entry) => {
    const file = path.join(root, entry.name);
    return entry.isDirectory() ? filesBelow(file) : [file];
  });
}

function stageSources(projectRoot, iosRoot, identifier, version, buildNumber) {
  const repo = path.resolve(projectRoot, '..');
  const native = path.join(projectRoot, 'native');
  const core = path.join(repo, 'android/app/src/main/cpp');
  const destination = path.join(iosRoot, TARGET);
  const required = [
    path.join(native, 'broadcast/SampleHandler.swift'),
    path.join(native, 'recognition/RecognitionBridge.h'),
    path.join(native, 'recognition/RecognitionBridge.mm'),
    path.join(native, 'recognition/TimerSession.cpp'),
    path.join(core, 'include/nt/engine.h'),
    path.join(repo, 'assets/templates/manifest.json'),
    path.join(repo, 'assets/avatars/index.json'),
    path.join(repo, 'internal/ocr/neuraldata/recognizer.onnx'),
  ];
  for (const file of required) if (!fs.existsSync(file)) throw new Error(`[withReplayKit] Required source/asset missing: ${file}`);
  // This is a generated, plugin-owned directory. Always refresh, including
  // removal of deleted sources/assets, without touching user native sources.
  fs.rmSync(destination, { recursive: true, force: true });
  fs.mkdirSync(destination, { recursive: true });
  fs.copyFileSync(required[0], path.join(destination, 'SampleHandler.swift'));
  fs.copyFileSync(path.join(projectRoot, 'modules/naruto-capture/ios/CaptureProtocol.swift'), path.join(destination, 'CaptureProtocol.swift'));
  fs.mkdirSync(path.join(destination, 'Recognition'), { recursive: true });
  for (const entry of fs.readdirSync(path.join(native, 'recognition'))) {
    if (/\.(h|hpp|mm|cpp)$/.test(entry)) fs.copyFileSync(path.join(native, 'recognition', entry), path.join(destination, 'Recognition', entry));
  }
  for (const directory of ['src', 'include']) fs.cpSync(path.join(core, directory), path.join(destination, 'Core', directory), { recursive: true });
  const assets = path.join(destination, 'RecognitionAssets');
  const assetSources = {
    templates: path.join(repo, 'assets/templates'), avatars: path.join(repo, 'assets/avatars'),
    game: path.join(repo, 'assets/game'), ninja_templates: path.join(repo, 'internal/ninja/templates'),
    ocr: path.join(repo, 'internal/ocr/neuraldata'),
  };
  for (const [name, root] of Object.entries(assetSources)) {
    fs.mkdirSync(path.join(assets, name), { recursive: true });
    for (const file of filesBelow(root)) {
      if (!/\.(png|json|onnx|md|txt)$/i.test(file) || /\.source\.png$/i.test(file)) continue;
      const target = path.join(assets, name, path.relative(root, file));
      fs.mkdirSync(path.dirname(target), { recursive: true });
      fs.copyFileSync(file, target); // Preserve original encoded PNG bytes/hash.
    }
  }
  fs.mkdirSync(path.join(assets, 'licenses'), { recursive: true });
  for (const name of ['LICENSE', 'THIRD_PARTY_NOTICES.md']) {
    fs.copyFileSync(path.join(repo, name), path.join(assets, 'licenses', name));
  }
  fs.writeFileSync(path.join(destination, `${TARGET}-Bridging-Header.h`), '#import "Recognition/RecognitionBridge.h"\n');
  fs.writeFileSync(path.join(destination, `${TARGET}-Info.plist`), plist.default.build({
    CFBundleDevelopmentRegion: '$(DEVELOPMENT_LANGUAGE)', CFBundleDisplayName: '替身计时器',
    CFBundleExecutable: '$(EXECUTABLE_NAME)', CFBundleIdentifier: identifier,
    CFBundleInfoDictionaryVersion: '6.0', CFBundleName: '$(PRODUCT_NAME)', CFBundlePackageType: 'XPC!',
    CFBundleShortVersionString: version, CFBundleVersion: buildNumber,
    NSExtension: {
      NSExtensionPointIdentifier: 'com.apple.broadcast-services-upload',
      NSExtensionPrincipalClass: '$(PRODUCT_MODULE_NAME).SampleHandler',
      NSExtensionAttributes: { RPBroadcastProcessMode: 'RPBroadcastProcessModeSampleBuffer' },
    },
  }));
  return filesBelow(destination).filter((file) => /\.(swift|mm|cpp)$/.test(file))
    .map((file) => path.relative(iosRoot, file).split(path.sep).join('/')).sort();
}

function configureProject(project, sources, identifier, team, version, buildNumber) {
  const objects = project.hash.project.objects;
  // node-xcode silently skips addTargetDependency unless BOTH sections exist.
  // The current Expo app template has neither section on its first prebuild.
  objects.PBXTargetDependency ||= {};
  objects.PBXContainerItemProxy ||= {};
  let entry = Object.entries(project.pbxNativeTargetSection()).find(([key, target]) => !key.endsWith('_comment') && unquote(target.name) === TARGET);
  if (!entry) {
    // xcode.addTarget installs the app dependency AND a PlugIns copy phase.
    const created = project.addTarget(TARGET, 'app_extension', TARGET, identifier);
    entry = [created.uuid, created.pbxNativeTarget];
  }
  const [targetID, target] = entry;
  const app = project.getFirstTarget();
  app.firstTarget.dependencies ||= [];
  if (!app.firstTarget.dependencies.some(({ value }) => objects.PBXTargetDependency[value]?.target === targetID)) {
    project.addTargetDependency(app.uuid, [targetID]);
  }
  const configs = objects.XCConfigurationList[target.buildConfigurationList].buildConfigurations;
  for (const { value } of configs) {
    Object.assign(objects.XCBuildConfiguration[value].buildSettings, {
      PRODUCT_BUNDLE_IDENTIFIER: quote(identifier), PRODUCT_NAME: quote(TARGET),
      PRODUCT_MODULE_NAME: TARGET, INFOPLIST_FILE: quote(`${TARGET}/${TARGET}-Info.plist`),
      SWIFT_OBJC_BRIDGING_HEADER: quote(`$(SRCROOT)/${TARGET}/${TARGET}-Bridging-Header.h`),
      IPHONEOS_DEPLOYMENT_TARGET: '16.4', TARGETED_DEVICE_FAMILY: quote('1,2'),
      APPLICATION_EXTENSION_API_ONLY: 'YES', SKIP_INSTALL: 'YES',
      SWIFT_VERSION: '5.0', CLANG_ENABLE_MODULES: 'YES', CLANG_ENABLE_OBJC_ARC: 'YES',
      CLANG_CXX_LANGUAGE_STANDARD: quote('c++17'), CLANG_CXX_LIBRARY: quote('libc++'),
      GCC_ENABLE_CPP_EXCEPTIONS: 'YES', GCC_ENABLE_CPP_RTTI: 'YES',
      GCC_OPTIMIZATION_LEVEL: '3', GCC_FAST_MATH: 'NO',
      OTHER_CPLUSPLUSFLAGS: [quote('$(inherited)'), quote('-fno-fast-math'), quote('-ffp-contract=off')],
      HEADER_SEARCH_PATHS: [quote('$(inherited)'), quote(`$(SRCROOT)/${TARGET}/Core/include`), quote(`$(SRCROOT)/${TARGET}/Recognition`)],
      LD_RUNPATH_SEARCH_PATHS: quote('$(inherited) @executable_path/Frameworks @executable_path/../../Frameworks'),
      CODE_SIGN_STYLE: 'Automatic', ENABLE_BITCODE: 'NO',
      MARKETING_VERSION: quote(version), CURRENT_PROJECT_VERSION: quote(buildNumber),
      GENERATE_INFOPLIST_FILE: 'NO',
    });
    if (team) objects.XCBuildConfiguration[value].buildSettings.DEVELOPMENT_TEAM = quote(team);
    // Intentionally no App Groups / provisioning entitlement or forced signing
    // override. Personal Team signing and unsigned CI are both supported.
  }

  function phase(isa, comment) {
    const found = target.buildPhases.find(({ value }) => objects[isa]?.[value]);
    return found ? objects[isa][found.value] : project.addBuildPhase([], isa, comment, targetID).buildPhase;
  }
  const refs = objects.PBXFileReference;
  const builds = objects.PBXBuildFile;
  function fileRef(file, type, sourceTree = 'SOURCE_ROOT') {
    const existing = Object.entries(refs).find(([key, value]) => !key.endsWith('_comment') && unquote(value.path) === file && unquote(value.sourceTree) === sourceTree);
    if (existing) { existing[1].lastKnownFileType = type; return existing[0]; }
    const id = project.generateUuid();
    refs[id] = { isa: 'PBXFileReference', lastKnownFileType: type, name: quote(path.basename(file)), path: quote(file), sourceTree: quote(sourceTree) };
    refs[`${id}_comment`] = path.basename(file);
    return id;
  }
  function buildFile(reference, name) {
    const existing = Object.entries(builds).find(([key, value]) => !key.endsWith('_comment') && value.fileRef === reference);
    if (existing) return { value: existing[0], comment: name };
    const id = project.generateUuid();
    builds[id] = { isa: 'PBXBuildFile', fileRef: reference, fileRef_comment: name };
    builds[`${id}_comment`] = name;
    return { value: id, comment: name };
  }
  const sourcePhase = phase('PBXSourcesBuildPhase', 'Sources');
  sourcePhase.files = sources.map((file) => buildFile(fileRef(file, file.endsWith('.swift') ? 'sourcecode.swift' : file.endsWith('.mm') ? 'sourcecode.cpp.objcpp' : 'sourcecode.cpp.cpp'), path.basename(file)));
  const resourcePhase = phase('PBXResourcesBuildPhase', 'Resources');
  resourcePhase.files = [buildFile(fileRef(`${TARGET}/RecognitionAssets`, 'folder'), 'RecognitionAssets')];
  const frameworks = ['Foundation', 'ReplayKit', 'Network', 'CoreMedia', 'CoreVideo', 'CoreImage', 'ImageIO', 'CoreGraphics', 'Vision'];
  phase('PBXFrameworksBuildPhase', 'Frameworks').files = frameworks.map((name) =>
    buildFile(fileRef(`System/Library/Frameworks/${name}.framework`, 'wrapper.framework', 'SDKROOT'), `${name}.framework`));

  // Explicitly verify/embed into the APP, never accidentally into another target.
  let embed = app.firstTarget.buildPhases.map(({ value }) => objects.PBXCopyFilesBuildPhase?.[value])
    .find((item) => item?.files?.some(({ value }) => builds[value]?.fileRef === target.productReference));
  if (!embed) {
    embed = project.addBuildPhase([], 'PBXCopyFilesBuildPhase', 'Embed App Extensions', app.uuid, 'app_extension').buildPhase;
    embed.files = [buildFile(target.productReference, `${TARGET}.appex`)];
  }
  embed.dstSubfolderSpec = 13;
  embed.dstPath = quote('');
  for (const { value } of embed.files) {
    if (builds[value]?.fileRef === target.productReference) {
      builds[value].settings = { ATTRIBUTES: ['RemoveHeadersOnCopy'] };
    }
  }
  return targetID;
}

function withReplayKit(config, options = {}) {
  const baseID = config.ios?.bundleIdentifier;
  if (!baseID) throw new Error('[withReplayKit] expo.ios.bundleIdentifier is required');
  const identifier = options.extensionBundleIdentifier || `${baseID}.broadcast`;
  if (!identifier.startsWith(`${baseID}.`) || !/^[A-Za-z0-9.-]+$/.test(identifier)) throw new Error('[withReplayKit] Extension identifier must be a child of the app bundle identifier');
  const version = config.version || '1.0.0';
  const buildNumber = config.ios?.buildNumber || '1';
  config = withInfoPlist(config, (mod) => {
    const info = mod.modResults;
    info.UIBackgroundModes = [...new Set([...(info.UIBackgroundModes || []), 'audio'])];
    info.NRTBroadcastExtensionIdentifier = identifier;
    info.NRTReplayKitBroadcastExtensionConfigured = true;
    delete info.NRTReplayKitBridgeStatus;
    return mod;
  });
  // Register the extension for EAS credential discovery, without App Groups.
  const build = config.extra?.eas?.build || {};
  const extensions = (build.experimental?.ios?.appExtensions || []).filter((extension) => extension.targetName !== TARGET);
  extensions.push({ targetName: TARGET, bundleIdentifier: identifier, entitlements: {} });
  config.extra = { ...config.extra, eas: { ...config.extra?.eas, build: { ...build,
    experimental: { ...build.experimental, ios: { ...build.experimental?.ios, appExtensions: extensions } },
  } } };
  return withXcodeProject(config, (mod) => {
    const sources = stageSources(mod.modRequest.projectRoot, mod.modRequest.platformProjectRoot, identifier, version, buildNumber);
    configureProject(mod.modResults, sources, identifier, config.ios?.appleTeamId, version, buildNumber);
    return mod;
  });
}

module.exports = createRunOncePlugin(withReplayKit, 'with-replay-kit', '1.0.0');
module.exports._test = { stageSources, configureProject };
