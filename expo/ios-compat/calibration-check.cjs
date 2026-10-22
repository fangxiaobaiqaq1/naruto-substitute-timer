// Run: cd expo && node ios-compat/calibration-check.cjs
// Existing TypeScript + Node only; no Expo runtime or test dependencies.
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const { test } = require('node:test');
const { runInNewContext } = require('node:vm');
const ts = require('typescript');
function load(file, requireModule = () => { throw Error('Unexpected dependency'); }) {
  const exports = {};
  const { outputText } = ts.transpileModule(readFileSync(join(__dirname, '..', file), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020, jsx: ts.JsxEmit.ReactJSX },
  });
  runInNewContext(outputText, { exports, require: requireModule });
  return exports;
}
const api = load('calibration.ts');
const plain = value => JSON.parse(JSON.stringify(value));
const beads = { x: 0.081, y: 0.104, width: 0.052, height: 0.022 };
const avatar = { x: 0.924, y: 0.006, width: 0.072, height: 0.128 };
const calibration = { profile: 'duel', leftBeads: beads, rightAvatar: avatar };
const scheme = (extra = {}) => JSON.stringify({ kind: 'naruto-timer-calibration', version: 1,
  frameWidth: 2400, frameHeight: 1080, ...calibration, ...extra });

test('interoperable schema: partial boxes, BOM, null/unknown fields, dimension hints, no runtime geometry', () => {
  const parsed = api.parseCalibration('\uFEFF' + scheme({ rightBeads: null, geometry: { rowDrop: 12 } }));
  assert.deepEqual(plain(parsed), { calibration, frameWidth: 2400, frameHeight: 1080 });
  const text = api.encodeCalibration(parsed.calibration, 2400, 1080);
  assert.deepEqual(JSON.parse(text), JSON.parse(scheme()));
  assert.deepEqual(plain(api.parseCalibration(text)), plain(parsed));
  assert.equal(api.calibrationAspectDifference(parsed, 1200, 540), 0);
  assert.ok(api.calibrationAspectDifference(parsed, 1600, 900) > 0.02);
  for (const side of [null, '2400', -1, 0, 1.5, 100001, true, {}]) {
    assert.equal(api.parseCalibration(scheme({ frameWidth: side })).frameWidth, 0);
  }
  assert.equal(api.parseCalibration(scheme({ frameWidth: 100000 })).frameWidth, 100000);
  assert.equal(api.calibrationAspectDifference({ ...parsed, frameWidth: 0 }, 1200, 540), null);
  assert.equal(api.calibrationAspectDifference(parsed, 0, 540), null);
  const edge = { x: 0.9, y: 0.1, width: 0.1, height: 0.03 };
  assert.deepEqual(plain(api.parseCalibration(scheme({ leftBeads: edge })).calibration.leftBeads), edge,
    'valid double coordinates must not be rounded or shrunk');
});

test('strict JSON, kind/version/profile/numeric bounds and 64 KiB UTF-8 cap', () => {
  for (const text of ['', '{}', '[]', 'null', scheme() + ' garbage', scheme() + '{}', '{"kind":NaN}']) {
    assert.throws(() => api.parseCalibration(text));
  }
  for (const extra of [{ kind: 'other' }, { version: '1' }, { version: null }, { version: 2 },
    { profile: 'auto' }, { profile: null }, { leftBeads: [] }, { leftBeads: '0,0,1,1' }]) {
    assert.throws(() => api.parseCalibration(scheme(extra)));
  }
  for (const field of ['x', 'y', 'width', 'height']) {
    for (const value of [undefined, null, '0.01', true, {}, -2]) {
      assert.throws(() => api.parseCalibration(scheme({ leftBeads: { ...beads, [field]: value } })));
    }
  }
  assert.throws(() => api.parseCalibration(scheme().replace('"x":0.081', '"x":1e999')));
  assert.throws(() => api.parseCalibration(scheme({ leftBeads: { ...beads, width: 0 } })));
  const base = scheme();
  assert.doesNotThrow(() => api.parseCalibration(base + ' '.repeat(65536 - Buffer.byteLength(base))));
  assert.throws(() => api.parseCalibration(base + ' '.repeat(65537 - Buffer.byteLength(base))), /64 KiB/);
  assert.throws(() => api.parseCalibration(scheme({ note: '豆'.repeat(23000) })), /64 KiB/);
  assert.throws(() => api.parseCalibration(scheme({ note: '😀'.repeat(17000) })), /64 KiB/);
  assert.throws(() => api.parseCalibration(' '.repeat(1000000)), /64 KiB/);
});

test('edge epsilon fits only tiny overflow; size boundaries remain exact', () => {
  const fitted = api.parseCalibration(scheme({ leftBeads: { x: -1e-6, y: 0.98, width: 0.08, height: 0.0200005 } })).calibration.leftBeads;
  assert.equal(fitted.x, 0);
  assert.ok(fitted.y + fitted.height <= 1);
  for (const b of [{ ...beads, x: -1.01e-6 }, { ...beads, x: 1 - beads.width + 1.01e-6 },
    { ...beads, y: 1 }, { ...beads, x: 2 }]) {
    assert.throws(() => api.parseCalibration(scheme({ leftBeads: b })));
  }
  for (const [width, height, past] of [[0.125, 0.04, 0.125 + 2 ** -55],
    [0.03125, 0.01, 0.03125 - 2 ** -58], [0.02197265625, 0.0390625, 0.02197265625 - 2 ** -58]]) {
    assert.equal(api.calibrationRectIssue('leftBeads', { x: 0, y: 0, width, height }), null);
    assert.notEqual(api.calibrationRectIssue('leftBeads', { x: 0, y: 0, width: past, height }), null);
  }
  for (const key of api.CALIBRATION_KEYS) {
    assert.notEqual(api.calibrationRectIssue(key, { x: 0, y: 0, width: 1, height: 1 }), null);
    assert.notEqual(api.calibrationRectIssue(key, { x: 0, y: 0, width: 0.001, height: 0.001 }), null);
  }
});

test('safe settings normalization drops bad regions independently, export refuses invalid edits', () => {
  for (const value of [null, undefined, [], 'bad', {}, { ...calibration, profile: 'unknown' }]) {
    assert.deepEqual(plain(api.normalizeCalibration(value)), { profile: 'duel' });
  }
  const input = { ...calibration, rightBeads: { x: 0, y: 0, width: 1, height: 1 }, leftAvatar: { ...avatar, x: NaN }, runtimeGeometry: {} };
  assert.deepEqual(plain(api.normalizeCalibration(input)), calibration);
  assert.ok(input.rightBeads, 'normalization must not mutate persisted input');
  assert.throws(() => api.encodeCalibration(input, 2400, 1080), /右侧豆/);
  assert.throws(() => api.encodeCalibration({ profile: 'bad' }, 0, 0));
  const empty = api.parseCalibration(api.encodeCalibration({ profile: 'camp' }, 0, 0));
  assert.deepEqual(plain(empty.calibration), { profile: 'camp' }, 'never invent automatic positions');
  const cleaned = api.normalizeCalibration(calibration);
  cleaned.leftBeads.x = 0;
  assert.equal(beads.x, 0.081, 'returned rectangles are independent copies');
});

test('400,000 size decisions match pinned Go/C++/Kotlin float64 and float32 digests', () => {
  const mask = (1n << 64n) - 1n;
  for (const f32 of [false, true]) {
    let seed = 20261021n, digest = 14695981039346656037n;
    function next() {
      seed = (seed + 0x9e3779b97f4a7c15n) & mask;
      let z = seed;
      z = ((z ^ (z >> 30n)) * 0xbf58476d1ce4e5b9n) & mask;
      z = ((z ^ (z >> 27n)) * 0x94d049bb133111ebn) & mask;
      return z ^ (z >> 31n);
    }
    const unit = () => Number(next() >> 11n) * 2 ** -53;
    for (let i = 0; i < 200000; i++) {
      const portrait = i % 2 === 1;
      const wn = portrait ? 68.5 / 960 : 0.0625, hn = portrait ? 68.5 / 540 : 1 / 36;
      const k = 0.2 + 2.2 * unit();
      let width = wn * k * (0.5 + unit()), height = hn * k * (0.5 + unit());
      switch (Number(next() % 8n)) {
        case 0: width = wn * 2; break;
        case 1: height = hn * 0.5; break;
        case 2: width = wn * 2; height = hn * 0.5; break;
      }
      let x = (1 - width) * unit(), y = (1 - height) * unit();
      switch (Number(next() % 16n)) {
        case 0: x = 1 - width; break;
        case 1: y = 1 - height + 1e-9; break;
      }
      if (f32) [x, y, width, height] = [x, y, width, height].map(Math.fround);
      let decision = 1;
      // Native engine has strict edges; the scheme parser intentionally fits epsilon overflow.
      if (x >= 0 && y >= 0 && width > 0 && height > 0 && x + width <= 1 && y + height <= 1) {
        const issue = api.calibrationRectIssue(portrait ? 'leftAvatar' : 'leftBeads', { x, y, width, height });
        decision = !issue ? 0 : issue.includes('太大') ? 2 : issue.includes('太小') ? 3 : 4;
      }
      digest = ((digest ^ BigInt(decision)) * 1099511628211n) & mask;
    }
    assert.equal(digest, f32 ? 0x5c07b936a6c3ad61n : 0xe9e1f0fa7bdcdb2dn);
  }
});

// A tiny hook/JSX harness tests editor transactions and gestures without loading RN.
function editorHarness(value = calibration) {
  const slots = []; let cursor = 0, effects = [], tree;
  const alerts = [], saved = [], shared = [];
  let closed = 0, document = scheme(), screenshot = { uri: 'file:///game.png', width: 2400, height: 1080 };
  const dimensions = { width: 1024, height: 768 };
  const react = {
    useState(initial) {
      const index = cursor++;
      if (!(index in slots)) slots[index] = typeof initial === 'function' ? initial() : initial;
      return [slots[index], value => { slots[index] = typeof value === 'function' ? value(slots[index]) : value; }];
    },
    useRef(initial) { const [ref] = react.useState(() => ({ current: initial })); return ref; },
    useMemo(fn, deps) {
      const index = cursor++, old = slots[index];
      if (!old || deps.some((dep, i) => dep !== old.deps[i])) slots[index] = { deps, value: fn() };
      return slots[index].value;
    },
    useEffect(fn, deps) {
      react.useMemo(() => { effects.push(fn); return null; }, deps);
    },
  };
  const jsx = (type, props) => ({ type, props });
  const rn = {
    Alert: { alert: (title, text, buttons) => alerts.push({ title, text, buttons }) },
    Image: 'Image', Pressable: 'Pressable', ScrollView: 'ScrollView', Text: 'Text', View: 'View',
    PanResponder: { create: handlers => ({ panHandlers: handlers }) },
    StyleSheet: { create: value => value, absoluteFill: {} }, useWindowDimensions: () => dimensions,
  };
  const component = load('CalibrationEditor.tsx', name => {
    if (name === 'react') return react;
    if (name === 'react/jsx-runtime') return { jsx, jsxs: jsx };
    if (name === 'react-native') return rn;
    if (name === 'react-native-safe-area-context') return { SafeAreaProvider: 'Provider', SafeAreaView: 'SafeArea' };
    if (name === './calibration') return api;
    throw Error(name);
  });
  const props = { value, onSave: value => saved.push(plain(value)), onClose: () => closed++,
    pickImage: async () => screenshot, importDocument: async () => document,
    shareDocument: async (name, text) => shared.push({ name, text }) };
  function expand(node) {
    if (Array.isArray(node)) return node.map(expand);
    if (!node || typeof node !== 'object') return node;
    if (typeof node.type === 'function') return expand(node.type(node.props));
    return { ...node, props: { ...node.props, children: expand(node.props.children) } };
  }
  function render() {
    cursor = 0; effects = [];
    tree = expand(component.CalibrationEditor(props));
    for (const effect of effects) effect();
    return tree;
  }
  function nodes(node) {
    if (Array.isArray(node)) return node.flatMap(nodes);
    if (!node || typeof node !== 'object') return [];
    return [node, ...nodes(node.props.children)];
  }
  const find = predicate => nodes(tree).find(predicate);
  async function flush() { for (let i = 0; i < 8; i++) await Promise.resolve(); render(); }
  async function press(label) {
    const button = find(node => node.type === 'Pressable' && node.props.accessibilityLabel === label);
    assert.ok(button, label);
    assert.ok(!button.props.disabled, `${label} enabled`);
    button.props.onPress(); await flush();
  }
  async function answer(accept) {
    const alert = alerts.at(-1); assert.ok(alert?.buttons, 'confirmation shown');
    alert.buttons[accept ? 1 : 0].onPress(); await flush();
  }
  async function loadImage() {
    await press('选择截图（文件）');
    find(node => typeof node.props.onLayout === 'function').props.onLayout({ nativeEvent: { layout: { width: 960 } } });
    render(); find(node => node.type === 'Image').props.onLoad(); render();
  }
  render();
  return { render, flush, press, answer, loadImage, find, alerts, saved, shared, dimensions,
    get closed() { return closed; }, set document(text) { document = text; }, set screenshot(value) { screenshot = value; } };
}

test('editor leaves automatic boxes absent, export does not save, cancel discards, explicit save submits', async () => {
  const untouched = editorHarness({ profile: 'duel' });
  await untouched.press('导出方案');
  assert.deepEqual(JSON.parse(untouched.shared[0].text), { kind: 'naruto-timer-calibration', version: 1, frameWidth: 0, frameHeight: 0, profile: 'duel' });
  assert.equal(untouched.saved.length, 0);
  await untouched.press('保存');
  assert.equal(untouched.saved.length, 0);
  assert.equal(untouched.closed, 1);

  const reset = editorHarness();
  await reset.press('清除所选'); await reset.answer(true);
  await reset.press('取消'); await reset.answer(false);
  assert.equal(reset.closed, 0);
  await reset.press('取消'); await reset.answer(true);
  assert.equal(reset.saved.length, 0);
  assert.equal(reset.closed, 1);

  const save = editorHarness();
  await save.press('全部恢复自动'); await save.answer(true); await save.press('保存');
  assert.deepEqual(save.saved, [{ profile: 'duel' }]);
});

test('editor import is pending, confirms aspect >2%, replaces missing boxes, ignores geometry', async () => {
  const editor = editorHarness();
  await editor.loadImage();
  editor.document = scheme({ profile: 'camp', frameWidth: 1600, frameHeight: 900, leftBeads: null,
    rightAvatar: avatar, geometry: { rowDrop: 99 } });
  await editor.press('导入方案');
  assert.equal(editor.saved.length, 0);
  await editor.press('导出方案');
  assert.deepEqual(api.parseCalibration(editor.shared[0].text).calibration, api.normalizeCalibration(calibration));
  await editor.press('应用到编辑器');
  assert.match(editor.alerts.at(-1).text, /超过 2%/);
  await editor.answer(false);
  await editor.press('应用到编辑器'); await editor.answer(true);
  assert.equal(editor.saved.length, 0);
  await editor.press('保存');
  assert.deepEqual(editor.saved, [{ profile: 'camp', rightAvatar: avatar }]);
});

test('editor draw/move/resize/stepper, invalid save rejection and rotation cancellation', async () => {
  const editor = editorHarness({ profile: 'duel' });
  await editor.loadImage();
  function gesture(dx, dy, finish = true) {
    const canvas = editor.find(node => node.props.onPanResponderGrant);
    const { width, height } = canvas.props.style;
    canvas.props.onPanResponderGrant({ nativeEvent: { locationX: width * 0.1, locationY: height * 0.1 } });
    canvas.props.onPanResponderMove({}, { dx: width * dx, dy: height * dy });
    if (finish) canvas.props.onPanResponderRelease();
    editor.render();
  }
  gesture(0.5, 0.5);
  await editor.press('保存');
  assert.equal(editor.saved.length, 0);
  assert.equal(editor.alerts.at(-1).title, '不能保存');
  gesture(0.0625, 0.025);
  await editor.press('移动'); gesture(0.01, 0.01);
  await editor.press('调大小'); gesture(0.005, 0.002);
  await editor.press('左侧豆左边 x增加 1 像素');
  await editor.press('导出方案');
  const before = api.parseCalibration(editor.shared.at(-1).text).calibration;
  assert.ok(before.leftBeads.x > 0.11);
  assert.ok(before.leftBeads.width > 0.0625);
  assert.equal(Object.keys(before).length, 2);
  gesture(0.01, 0.01, false);
  editor.dimensions.height = 500;
  editor.render(); editor.render();
  await editor.press('导出方案');
  assert.deepEqual(api.parseCalibration(editor.shared.at(-1).text).calibration, before, 'rotation cancels interrupted gesture');
  await editor.press('保存');
  assert.deepEqual(editor.saved, [plain(before)]);
});
