// Run: cd expo && node --test --test-reporter=dot ios-compat/controls-check.cjs
// Real App -> TimerScreen and replayKit; only React/RN, storage and native I/O are faked.
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const { test } = require('node:test');
const { runInNewContext } = require('node:vm');
const ts = require('typescript');
const plain = value => JSON.parse(JSON.stringify(value));
const compiled = new Map();
function load(file, mocks, globals = {}) {
  if (!compiled.has(file)) compiled.set(file, ts.transpileModule(readFileSync(join(__dirname, '..', file), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020, jsx: ts.JsxEmit.ReactJSX },
  }).outputText);
  const exports = {};
  runInNewContext(compiled.get(file), { exports, require(name) {
    if (name in mocks) return mocks[name];
    throw Error(`Unexpected import ${name} in ${file}`);
  }, ...globals }, { filename: file });
  return exports;
}
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const controls = (extra = {}) => ({ playerSide: 'left', showBothSides: false, rememberSide: true,
  initialized: true, revision: 1, ...extra });
const snapshot = (value = controls()) => ({ available: true, prepared: true, pipSupported: true,
  pipActive: true, broadcastActive: true, connected: true, message: 'native', controls: value });
const settingsKey = 'naruto-timer-ios-settings-v1';

function harness(t, { nativeControls = controls(), stored = {}, storageRead } = {}) {
  const slots = [], timers = new Map(), nativeListeners = new Set(), appListeners = new Set();
  const calls = { get: 0, bulk: [], controls: [], writes: [], removed: 0 };
  let cursor = 0, effects = [], dirty = false, tree, mounted = true, renders = 0, lateUpdates = 0;
  let clock = 0, timerID = 0, state = snapshot(nativeControls), nextCommand, nextRead;
  const sameDeps = (a, b) => a && b && a.length === b.length && a.every((value, i) => Object.is(value, b[i]));
  const react = {
    useState(initial) {
      const index = cursor++;
      if (!(index in slots)) slots[index] = typeof initial === 'function' ? initial() : initial;
      return [slots[index], value => {
        if (!mounted) { lateUpdates++; return; }
        const next = typeof value === 'function' ? value(slots[index]) : value;
        if (!Object.is(next, slots[index])) { slots[index] = next; dirty = true; }
      }];
    },
    useRef(initial) { return react.useState(() => ({ current: initial }))[0]; },
    useMemo(fn, deps) {
      const index = cursor++, old = slots[index];
      if (!old || !sameDeps(old.deps, deps)) slots[index] = { deps, value: fn() };
      return slots[index].value;
    },
    useCallback(fn, deps) { return react.useMemo(() => fn, deps); },
    useEffect(fn, deps) {
      const index = cursor++, old = slots[index];
      if (!old || !sameDeps(old.deps, deps)) {
        const slot = { deps, cleanup: old?.cleanup, effect: true };
        slots[index] = slot;
        effects.push(() => { slot.cleanup?.(); slot.cleanup = fn(); });
      }
    },
  };
  const globals = {
    setTimeout(fn, delay) { const id = ++timerID; timers.set(id, { fn, at: clock + delay }); return id; },
    clearTimeout(id) { timers.delete(id); },
  };
  const AppState = { currentState: 'active', addEventListener(event, listener) {
    assert.equal(event, 'change'); appListeners.add(listener);
    return { remove() { appListeners.delete(listener); } };
  } };
  const native = {
    getState() { calls.get++; const response = nextRead; nextRead = undefined; return response ? response.promise : state; },
    async prepare() { return state; }, async startPictureInPicture() { return state; }, async stop() { return state; },
    async setSettings(value) {
      calls.bulk.push(plain(value));
      // Native bulk settings restore only migrates controls once, never undoes PiP commands.
      if (!state.controls?.initialized) state = snapshot(controls({ playerSide: value.playerSide,
        showBothSides: value.showBothSides, rememberSide: value.rememberSide, revision: 1 }));
      return state;
    },
    async setControls(patch) {
      calls.controls.push(plain(patch));
      const response = nextCommand; nextCommand = undefined;
      if (response) {
        const result = await response.promise;
        if (result.controls?.revision >= state.controls?.revision) state = result;
        return result;
      }
      state = snapshot({ ...state.controls, ...patch, initialized: true, revision: state.controls.revision + 1 });
      return state;
    },
    addListener(event, listener) {
      assert.equal(event, 'onState'); nativeListeners.add(listener);
      return { remove() { calls.removed++; nativeListeners.delete(listener); } };
    },
  };
  const rn = { AppState, Platform: { OS: 'ios', Version: '18' }, Alert: { alert() {} }, Linking: { openURL: async () => {} },
    StyleSheet: { create: value => value }, useWindowDimensions: () => ({ width: 390, height: 844, fontScale: 1 }) };
  for (const name of ['Modal', 'Pressable', 'ScrollView', 'Switch', 'Text', 'TextInput', 'View']) rn[name] = name;
  const calibration = load('calibration.ts', {});
  const api = load('replayKit.ts', { expo: { requireOptionalNativeModule: () => native, requireNativeView: () => 'NativeCaptureView' },
    'react-native': rn, './calibration': calibration }, globals);
  const jsx = (type, props) => ({ type, props: props || {} });
  const app = load('App.tsx', {
    react, 'react/jsx-runtime': { jsx, jsxs: jsx, Fragment: 'Fragment' }, 'react-native': rn,
    'expo-status-bar': { StatusBar: 'StatusBar' },
    'react-native-safe-area-context': { SafeAreaProvider: 'SafeAreaProvider', SafeAreaView: 'SafeAreaView' },
    '@react-native-async-storage/async-storage': { __esModule: true, default: {
      getItem(key) { assert.equal(key, settingsKey); return storageRead?.promise ?? Promise.resolve(JSON.stringify({ autoCheckUpdates: false, ...stored })); },
      async setItem(key, value) { assert.equal(key, settingsKey); calls.writes.push(JSON.parse(value)); },
    } },
    './replayKit': api, './ios-compat/useCountdownNow': { useCountdownNow: () => 1000 },
    './CalibrationEditor': { CalibrationEditor: 'CalibrationEditor' },
    './updates': { APP_VERSION: 'test', isDualNinja: () => false, checkForUpdate: async () => ({ newer: false, version: 'test', notes: '' }) },
  }, globals);
  function expand(node) {
    if (Array.isArray(node)) return node.map(expand);
    if (!node || typeof node !== 'object') return node;
    if (typeof node.type === 'function') return expand(node.type(node.props));
    if (node.type === 'Modal' && !node.props.visible) return null;
    return { ...node, props: { ...node.props, children: expand(node.props.children) } };
  }
  function render() {
    assert.ok(mounted);
    renders++; cursor = 0; effects = []; dirty = false;
    tree = expand(app.default());
    for (const effect of effects) effect();
  }
  function nodes(node) {
    if (Array.isArray(node)) return node.flatMap(node => nodes(node));
    return node && typeof node === 'object' ? [node, ...nodes(node.props.children)] : [];
  }
  function text(node) {
    if (Array.isArray(node)) return node.map(text).join('');
    if (!node || typeof node !== 'object') return node ?? '';
    return text(node.props.children);
  }
  function find(label, type) {
    const node = nodes(tree).find(node => (!type || node.type === type) &&
      (node.props.accessibilityLabel === label || text(node) === label));
    assert.ok(node, `UI node: ${label}`); return node;
  }
  async function flush() {
    // Drain promises and queued React commits, but never silently run timers.
    for (let i = 0; i < 40; i++) { await Promise.resolve(); if (dirty && mounted) render(); }
    assert.equal(dirty && mounted, false, 'effects must settle without a render loop');
  }
  async function advance(ms) {
    const until = clock + ms;
    for (let i = 0; ; i++) {
      const entry = [...timers].filter(([, value]) => value.at <= until).sort((a, b) => a[1].at - b[1].at)[0];
      if (!entry) break;
      assert.ok(i < 100, 'timers must not loop');
      clock = entry[1].at; timers.delete(entry[0]); entry[1].fn(); await flush();
    }
    clock = until; await flush();
  }
  function unmount() {
    if (!mounted) return;
    mounted = false;
    for (const slot of slots) if (slot?.effect) slot.cleanup?.();
  }
  t.after(unmount);
  render();
  return { api, calls, flush, advance, find, unmount,
    get renders() { return renders; }, get lateUpdates() { return lateUpdates; },
    get resources() { return { timers: timers.size, native: nativeListeners.size, app: appListeners.size }; },
    get mirror() { return calls.writes.at(-1); },
    get alerts() { return nodes(tree).filter(node => node.props.accessibilityRole === 'alert').map(text); },
    get side() { return find('我方右边', 'Pressable').props.accessibilityState.selected ? 'right' : 'left'; },
    async press(label) { const node = find(label, 'Pressable'); assert.ok(!node.props.disabled, `${label} enabled`); node.props.onPress(); await flush(); },
    async toggle(label, value) { const node = find(label, 'Switch'); assert.ok(!node.props.disabled, `${label} enabled`); node.props.onValueChange(value); await flush(); },
    async emit(value) { state = value; for (const listener of nativeListeners) listener(value); await flush(); },
    async appState(value) { AppState.currentState = value; for (const listener of appListeners) listener(value); await flush(); },
    deferCommand() { nextCommand = deferred(); return nextCommand; },
    deferRead() { nextRead = deferred(); return nextRead; },
  };
}

async function ready(t, options) {
  const h = harness(t, options); await h.flush(); await h.advance(250); return h;
}

test('slow stale storage cannot overwrite already initialized native controls', async t => {
  const storageRead = deferred();
  const h = harness(t, { storageRead, nativeControls: controls({ playerSide: 'right', showBothSides: true, rememberSide: false, revision: 8 }) });
  await h.flush();
  assert.equal(h.side, 'right', 'native subscription wins before storage resolves');
  assert.equal(h.calls.bulk.length, 0);
  storageRead.resolve(JSON.stringify({ playerSide: 'left', showBothSides: false, rememberSide: true, fontScale: 1.1, autoCheckUpdates: false }));
  await h.flush();
  assert.equal(h.calls.bulk.length, 0, 'initialized native state needs no startup migration');
  await h.advance(250);
  assert.equal(h.side, 'right');
  assert.equal(h.mirror.playerSide, 'right'); assert.equal(h.mirror.showBothSides, true); assert.equal(h.mirror.rememberSide, false);
  assert.equal(h.mirror.fontScale, 1.1, 'unrelated saved preferences still restore');
  assert.deepEqual(h.calls.controls, [], 'restoration is never a user command');
});

test('only uninitialized native controls migrate saved settings at startup', async t => {
  const h = harness(t, { nativeControls: controls({ initialized: false, revision: 0 }),
    stored: { playerSide: 'right', showBothSides: true, rememberSide: true } });
  await h.flush();
  assert.equal(h.calls.bulk.length, 1, 'migration precedes the debounced settings effect');
  assert.equal(h.calls.bulk[0].playerSide, 'right'); assert.equal(h.calls.bulk[0].showBothSides, true);
  assert.equal(h.side, 'right'); assert.equal(h.mirror.showBothSides, true);
  await h.advance(250);
  assert.deepEqual(h.calls.controls, []);
});

test('missing native controls are not evidence that startup migration is safe', async t => {
  const h = harness(t, { nativeControls: null, stored: { playerSide: 'right', showBothSides: true } });
  await h.flush();
  assert.equal(h.calls.bulk.length, 0, 'only explicit initialized=false permits startup migration, not a failed/unknown snapshot');
  await h.advance(250);
  assert.equal(h.calls.bulk.length, 0, 'debounced bulk settings must also wait for known native control state');
  await h.emit(snapshot(controls({ playerSide: 'left', showBothSides: false, revision: 8 })));
  await h.advance(250);
  assert.equal(h.side, 'left');
  assert.equal(h.calls.bulk[0].playerSide, 'left', 'later native recovery is authoritative');
  assert.deepEqual(h.calls.controls, []);
});

test('foreground refresh adopts game-side PiP changes and mirrors them without control commands', async t => {
  const h = await ready(t);
  await h.appState('background');
  await h.emit(snapshot(controls({ playerSide: 'right', showBothSides: true, revision: 20 })));
  assert.equal(h.side, 'left', 'suspended JS must not render background native events');
  await h.appState('active');
  assert.equal(h.side, 'right'); assert.equal(h.mirror.playerSide, 'right'); assert.equal(h.mirror.showBothSides, true);
  await h.press('打开替身设置');
  assert.equal(h.find('两边计时', 'Switch').props.value, true);
  await h.advance(250);
  assert.deepEqual(h.calls.controls, [], 'foreground adoption and persistence must never call setControls');
});

test('older revisions cannot undo UI controls or their persisted mirror', async t => {
  const h = await ready(t);
  await h.emit(snapshot(controls({ playerSide: 'right', showBothSides: true, rememberSide: false, revision: 20 })));
  await h.advance(250);
  const writes = h.calls.writes.length;
  await h.emit(snapshot(controls({ revision: 19 })));
  await h.advance(250);
  assert.equal(h.side, 'right'); assert.equal(h.mirror.showBothSides, true); assert.equal(h.mirror.rememberSide, false);
  assert.equal(h.calls.writes.length, writes, 'old native controls cannot trigger a persistence rollback');
  assert.deepEqual(h.calls.controls, []);
});

test('duplicate control snapshots preserve settings identity and do not cause effect/send loops', async t => {
  const h = await ready(t, { nativeControls: controls({ playerSide: 'right', revision: 3 }) });
  const before = { writes: h.calls.writes.length, bulk: h.calls.bulk.length, renders: h.renders };
  for (let i = 0; i < 4; i++) {
    // Capture telemetry can change; identical controls must not recreate settings.
    await h.emit({ ...snapshot(controls({ playerSide: 'right', revision: 3 })), frames: i + 1 });
    await h.advance(250);
  }
  assert.equal(h.calls.writes.length, before.writes);
  assert.equal(h.calls.bulk.length, before.bulk);
  assert.ok(h.renders - before.renders <= 4, 'at most one telemetry render per event, no controls rerender loop');
  assert.deepEqual(h.calls.controls, []);
});

test('explicit left/right, both-side and remember switches use acknowledged dedicated patches', async t => {
  const h = await ready(t);
  const pending = h.deferCommand(), bulk = h.calls.bulk.length;
  await h.press('我方右边');
  assert.deepEqual(h.calls.controls, [{ playerSide: 'right' }]);
  assert.equal(h.calls.bulk.length, bulk, 'explicit command is not a bulk settings call');
  assert.equal(h.side, 'left', 'no optimistic guessed success before acknowledgment');
  assert.equal(h.find('我方右边', 'Pressable').props.disabled, true);
  pending.resolve(snapshot(controls({ playerSide: 'right', revision: 2 })));
  await h.flush(); assert.equal(h.side, 'right');
  await h.press('打开替身设置');
  await h.press('我方位置：左边');
  await h.toggle('两边计时', true);
  await h.toggle('记住我方位置', false);
  assert.deepEqual(h.calls.controls, [{ playerSide: 'right' }, { playerSide: 'left' }, { showBothSides: true }, { rememberSide: false }]);
  assert.equal(h.side, 'left'); assert.equal(h.find('两边计时', 'Switch').props.value, true);
  assert.equal(h.find('记住我方位置', 'Switch').props.value, false);
  await h.advance(250);
  assert.equal(h.calls.controls.length, 4, 'effects do not replay explicit commands');
});

test('compact swap and both-side callbacks use the same dedicated control path', async t => {
  const h = await ready(t, { stored: { miniMode: true } });
  const pill = h.find('自动计时·我方左（手动选择）：未计时。点击打开控制', 'Pressable');
  pill.props.onPress(); await h.flush();
  await h.press('换边'); await h.press('两边计时');
  assert.deepEqual(h.calls.controls, [{ playerSide: 'right' }, { showBothSides: true }]);
  assert.equal(h.side, 'right'); assert.equal(h.mirror.showBothSides, true);
  await h.advance(250);
  assert.equal(h.calls.controls.length, 2);
});

test('a delayed command acknowledgment cannot undo a newer PiP controls event', async t => {
  const h = await ready(t);
  const pending = h.deferCommand();
  await h.press('我方右边');
  await h.emit(snapshot(controls({ playerSide: 'left', showBothSides: true, revision: 30 })));
  pending.resolve(snapshot(controls({ playerSide: 'right', revision: 2 }))); await h.flush();
  assert.equal(h.side, 'left'); assert.equal(h.mirror.showBothSides, true);
  assert.equal(h.find('我方右边', 'Pressable').props.disabled, false);
  assert.deepEqual(h.calls.controls, [{ playerSide: 'right' }]);
});

test('failed or unconfirmed commands leave selected controls unchanged and display an error', async t => {
  const h = await ready(t);
  const failed = h.deferCommand();
  await h.press('我方右边'); failed.reject(new Error('native command failed')); await h.flush();
  assert.equal(h.side, 'left'); assert.ok(h.alerts.length, 'rejected command is visible, not silent success');
  assert.equal(h.find('我方右边', 'Pressable').props.disabled, false, 'failure releases command gate');
  const missing = h.deferCommand();
  await h.press('我方右边'); missing.resolve(snapshot(null)); await h.flush();
  assert.equal(h.side, 'left'); assert.ok(h.alerts.length, 'missing authoritative acknowledgment is also an error');
  await h.advance(250);
  assert.equal(h.mirror.playerSide, 'left'); assert.equal(h.calls.controls.length, 2);
});

test('unmount cleans subscriptions/timers and ignores a late storage/bootstrap response', async t => {
  const storageRead = deferred();
  const h = harness(t, { storageRead, nativeControls: controls({ initialized: false, revision: 0 }) });
  await h.flush();
  h.unmount();
  assert.deepEqual(h.resources, { timers: 0, native: 0, app: 0 });
  storageRead.resolve(JSON.stringify({ playerSide: 'right' })); await h.flush();
  assert.equal(h.calls.bulk.length, 0); assert.equal(h.calls.writes.length, 0); assert.equal(h.lateUpdates, 0);
  assert.equal(h.calls.removed, 1);
});

test('unmount cancels the pending initial bulk-settings debounce', async t => {
  const h = harness(t); await h.flush();
  assert.equal(h.calls.bulk.length, 0);
  h.unmount(); await h.advance(250);
  assert.equal(h.calls.bulk.length, 0, 'cleaned-up effects never send native settings');
  assert.deepEqual(h.resources, { timers: 0, native: 0, app: 0 });
  assert.equal(h.lateUpdates, 0);
});

test('pending foreground refresh cannot update an unmounted TimerScreen', async t => {
  const h = await ready(t);
  await h.appState('background');
  const read = h.deferRead();
  await h.appState('active'); h.unmount();
  read.resolve(snapshot(controls({ playerSide: 'right', revision: 50 }))); await h.flush();
  assert.deepEqual(h.resources, { timers: 0, native: 0, app: 0 });
  assert.equal(h.lateUpdates, 0); assert.equal(h.mirror.playerSide, 'left');
});
