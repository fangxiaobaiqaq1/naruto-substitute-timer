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
  startPictureInPicture: async () => ({}), stop: async () => ({}), setSettings: async () => {}, ...extra });

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
  assert.equal(settings.playerSide, 'auto'); assert.equal(settings.rememberSide, false);
  assert.equal(settings.roiScale, 1.4); assert.equal(settings.roiOffsetX, -0.15);
  assert.equal(settings.backgroundFPS, 10); assert.equal(Array.from(settings.playerName).length, 40);
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

test('special ninja and semantic version rules match v0.3.0', () => {
  const api = load(path.join(__dirname, '../updates.ts'));
  assert.equal(api.isDualNinja('照美冥【五代目水影】'), true);
  assert.equal(api.isDualNinja('半藏[秽土转生]'), true);
  for (const name of ['', '半藏', '照美冥', '千手柱间[秽土转生]']) assert.equal(api.isDualNinja(name), false);
  assert.equal(api.isNewerVersion('v0.10.0'), true);
  assert.equal(api.isNewerVersion('v0.3.0'), false);
  assert.equal(api.isNewerVersion('v0.2.99'), false);
  assert.equal(api.isNewerVersion('javascript:alert(1)'), false);
  assert.equal(api.isNewerVersion('v1.0.0-beta'), false);
});
