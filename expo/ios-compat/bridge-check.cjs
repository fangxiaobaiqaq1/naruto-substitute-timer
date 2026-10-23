const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const { runInNewContext } = require('node:vm');
const ts = require('typescript');

function load(file, mocks = {}, globals = {}) {
  const exports = {};
  const source = ts.transpileModule(readFileSync(file, 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  }).outputText;
  runInNewContext(source, { exports, require(name) {
    if (name in mocks) return mocks[name];
    if (name.startsWith('.')) return load(path.resolve(path.dirname(file), name + '.ts'), mocks, globals);
    throw new Error(`Unexpected import ${name}`);
  }, setTimeout, clearTimeout, ...globals });
  return exports;
}

function bridge(native, globals = {}) {
  const listeners = new Set();
  const AppState = { currentState: 'active', addEventListener(_event, listener) {
    listeners.add(listener); return { remove() { listeners.delete(listener); } };
  } };
  return { AppState, listeners, api: load(path.join(__dirname, '../replayKit.ts'), {
    expo: { requireOptionalNativeModule: () => native, requireNativeView: () => function NativeView() {} },
    'react-native': { AppState, Platform: { OS: 'ios' } },
  }, globals) };
}
const nativeModule = (extra = {}) => ({ getState: () => ({}), prepare: async () => ({}),
  startPictureInPicture: async () => ({}), stop: async () => ({}), setSettings: async () => {}, setControls: async () => ({}), ...extra });

test('no native module is unavailable, never success-shaped', async () => {
  const { api } = bridge(null);
  assert.equal(api.hasNativeCapture, false);
  assert.equal((await api.getCaptureState()).available, false);
  assert.equal(api.normalizeCaptureState({ available: true, pipActive: true }).pipActive, false);
  await assert.rejects(api.prepareCapture());
});

test('untrusted native state and persisted settings are bounded', () => {
  const { api } = bridge(nativeModule());
  const state = api.normalizeCaptureState({ available: true, pipSupported: false, pipActive: true,
    leftDeadlineMs: Infinity, rightDeadlineMs: -1, leftCount: 7, rightCount: 2.5,
    frames: NaN, latencyMs: -10, frameWidth: Infinity, leftSubstitutions: -1 });
  assert.equal(state.pipActive, false);
  for (const key of ['leftDeadlineMs', 'rightDeadlineMs', 'frames', 'frameWidth', 'latencyMs', 'leftSubstitutions']) assert.equal(state[key], 0);
  assert.equal(state.leftCount, -1); assert.equal(state.rightCount, -1);
  const settings = api.normalizeCaptureSettings({ playerSide: 'guess', rememberSide: false,
    fontScale: Infinity, roiScale: 100, roiOffsetX: -8, backgroundFPS: 100,
    playerName: '🐈'.repeat(45), ninjaQuery: ' 半藏[秽土转生] ', calibration: { profile: 'bad' } });
  assert.equal(settings.playerSide, 'left'); assert.equal(settings.rememberSide, false);
  assert.equal(api.normalizeCaptureSettings({ playerSide: 'right' }).playerSide, 'right');
  assert.equal(api.normalizeCaptureSettings({ playerSide: 'auto' }).playerSide, 'left');
  assert.equal('playerName' in settings, false, 'legacy account data never reaches iOS native');
  assert.equal('playerNames' in settings, false);
  assert.equal(settings.roiScale, 1.4); assert.equal(settings.roiOffsetX, -0.15);
  assert.equal(settings.backgroundFPS, 10);
  assert.equal(settings.ninjaQuery, '半藏[秽土转生]'); assert.equal(settings.calibration.profile, 'duel');
  const good = api.normalizeCaptureState({ available: true, pipSupported: true, pipActive: true,
    leftCount: 6, rightCount: 0, leftDeadlineMs: 1, leftAlternateDeadlineMs: 2, leftNinja: '半藏[秽土转生]' });
  assert.equal(good.leftCount, 6); assert.equal(good.rightCount, 0); assert.equal(good.leftDeadlineMs, 1);
  assert.equal(good.leftAlternateDeadlineMs, 2, 'expired deadlines are not erased by the JS bridge');
});

test('events beat older requests and foreground watchdog/lifecycle clean up', async () => {
  let resolve, event, removed = false, tickId = 0;
  const timers = new Map(), updates = [];
  const { api, AppState, listeners } = bridge(nativeModule({
    getState: () => new Promise((r) => { resolve = r; }),
    addListener: (_name, listener) => { event = listener; return { remove() { removed = true; } }; },
  }), { setTimeout: (fn) => { timers.set(++tickId, fn); return tickId; }, clearTimeout: (id) => timers.delete(id) });
  const stop = api.subscribeCaptureState((state) => updates.push(state));
  assert.equal(timers.size, 1);
  event({ available: true, frames: 20 });
  resolve({ available: true, frames: 1 });
  await new Promise(setImmediate);
  assert.equal(updates.at(-1).frames, 20, 'late polling result cannot overwrite a native event');
  AppState.currentState = 'background'; for (const listener of listeners) listener('background');
  assert.equal(timers.size, 0);
  event({ available: true, frames: 21 });
  assert.equal(updates.at(-1).frames, 20, 'no background JS rendering');
  AppState.currentState = 'active'; for (const listener of listeners) listener('active');
  resolve({ available: true, frames: 22 }); await new Promise(setImmediate);
  assert.equal(updates.at(-1).frames, 22);
  stop(); assert.equal(timers.size, 0); assert.equal(listeners.size, 0); assert.equal(removed, true);
});

test('control state is strict and merges only native-owned fields without echo churn', () => {
  const { api } = bridge(nativeModule());
  const controls = { initialized: true, revision: 4, playerSide: 'right', showBothSides: true, rememberSide: false };
  for (const invalid of [null, {}, { ...controls, playerSide: 'auto' }, { ...controls, revision: Infinity },
    { ...controls, revision: -1 }, { ...controls, revision: 1.5 }, { ...controls, initialized: 'yes' }, { ...controls, showBothSides: 1 }]) {
    assert.equal(api.normalizeCaptureControls(invalid), null);
  }
  const normalized = api.normalizeCaptureState({ available: true, controls }).controls;
  assert.equal(normalized.playerSide, 'right'); assert.equal(normalized.revision, 4);
  const old = { playerSide: 'left', showBothSides: false, rememberSide: true, fontScale: 1.1, calibration: { profile: 'camp' } };
  assert.equal(api.mergeCaptureControls(old, null), old);
  assert.equal(api.mergeCaptureControls(old, { ...controls, initialized: false }), old, 'uninitialized native defaults cannot erase legacy settings');
  const updated = api.mergeCaptureControls(old, normalized);
  assert.equal(updated.playerSide, 'right'); assert.equal(updated.showBothSides, true); assert.equal(updated.rememberSide, false);
  assert.equal(updated.fontScale, old.fontScale); assert.equal(updated.calibration, old.calibration);
  assert.equal(api.mergeCaptureControls(updated, normalized), updated, 'duplicate state keeps React object identity');
});

test('explicit control patches validate and await native acknowledgement', async () => {
  const sent = [];
  const controls = { initialized: true, revision: 5, playerSide: 'right', showBothSides: false, rememberSide: true };
  const { api } = bridge(nativeModule({ setControls: async (patch) => { sent.push(patch); return { available: true, controls }; } }));
  const state = await api.setCaptureControls({ playerSide: 'right', fontScale: 100, playerName: 'ignored' });
  assert.equal(JSON.stringify(sent[0]), '{"playerSide":"right"}'); assert.equal(state.controls.revision, 5);
  await api.setCaptureControls({ showBothSides: false });
  assert.equal(JSON.stringify(sent[1]), '{"showBothSides":false}');
  for (const invalid of [{}, { playerSide: 'auto' }, { showBothSides: 'false' }, { rememberSide: 1 }]) await assert.rejects(api.setCaptureControls(invalid));
  assert.equal(sent.length, 2, 'invalid control commands never reach native');
  const missingAck = bridge(nativeModule()).api;
  await assert.rejects(missingAck.setCaptureControls({ playerSide: 'left' }), /原生未确认/);
});

test('special ninja and semantic version rules match v0.3.0', () => {
  const api = load(path.join(__dirname, '../updates.ts'));
  const config = JSON.parse(readFileSync(path.join(__dirname, '../app.json'), 'utf8')).expo;
  assert.equal(api.APP_VERSION, config.version);
  assert.equal(String(api.IOS_BUILD), config.ios.buildNumber);
  assert.equal(api.isDualNinja('照美冥【五代目水影】'), true);
  assert.equal(api.isDualNinja('半藏[秽土转生]'), true);
  for (const name of ['', '半藏', '照美冥', '千手柱间[秽土转生]']) assert.equal(api.isDualNinja(name), false);
  assert.equal(api.isNewerVersion('v0.10.0'), true);
  assert.equal(api.isNewerVersion('v0.3.0'), false);
  assert.equal(api.isNewerVersion('v0.2.99'), false);
  assert.equal(api.isNewerVersion('javascript:alert(1)'), false);
  assert.equal(api.isNewerVersion('v1.0.0-beta'), false);
});
