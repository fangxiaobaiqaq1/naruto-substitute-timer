// Run from expo/: node ios-compat/check.cjs (Node + the existing TypeScript only).
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const { test } = require('node:test');
const { runInNewContext } = require('node:vm');
const ts = require('typescript');

// Exercise the real hook with a fake wall clock/AppState, without loading native RN.
test('countdown sleeps while idle/background, resumes wall time, and cleans up', () => {
  let time = 1_000;
  let sequence = 0;
  let previousDeps, pendingEffect, cleanup;
  const updates = [];
  const timers = new Map();
  const listeners = new Set();
  const AppState = {
    currentState: 'active',
    addEventListener(event, listener) {
      assert.equal(event, 'change');
      listeners.add(listener);
      return { remove: () => listeners.delete(listener) };
    },
  };
  const react = {
    useState: () => [undefined, (value) => updates.push(value)],
    useEffect(effect, deps) {
      if (!previousDeps || deps.some((value, index) => value !== previousDeps[index])) pendingEffect = effect;
      previousDeps = deps;
    },
  };
  const exports = {};
  const { outputText } = ts.transpileModule(readFileSync(join(__dirname, 'useCountdownNow.ts'), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
  });
  runInNewContext(outputText, {
    exports,
    require: (name) => {
      if (name === 'react') return react;
      if (name === 'react-native') return { AppState };
      throw new Error(`Unexpected dependency: ${name}`);
    },
    Date: { now: () => time },
    setTimeout(callback, delay) {
      assert.ok(delay > 0 && delay <= 100);
      const id = ++sequence;
      timers.set(id, { callback, due: time + delay });
      return id;
    },
    clearTimeout: (id) => timers.delete(id),
  });
  const render = (deadline) => {
    const renderedTime = exports.useCountdownNow(deadline);
    if (pendingEffect) {
      cleanup?.();
      cleanup = pendingEffect();
      pendingEffect = undefined;
    }
    assert.equal(listeners.size, 1, 'one AppState subscription after rerender');
    return renderedTime;
  };
  const changeState = (state) => {
    AppState.currentState = state;
    for (const listener of listeners) listener(state);
  };
  const advance = (milliseconds) => {
    time += milliseconds;
    // A delayed JS callback must use current wall time, not replay missed ticks.
    for (const [id, timer] of [...timers]) {
      if (timer.due <= time) {
        timers.delete(id);
        timer.callback();
      }
    }
  };

  render(0);
  const idleUpdates = updates.length;
  advance(60_000);
  assert.equal(timers.size, 0);
  assert.equal(updates.length, idleUpdates, 'idle must not render periodically');
  const primary = time + 15_000;
  const alternate = time + 10_000;
  assert.equal(render(primary), time, 'starting after idle must not display >15 seconds');
  assert.equal(timers.size, 1);
  advance(5_000);
  assert.equal(updates.at(-1), time);
  assert.equal(primary - updates.at(-1), 10_000);
  assert.equal(alternate - updates.at(-1), 5_000);

  changeState('inactive');
  assert.equal(timers.size, 0, 'pause even before background');
  changeState('background');
  const pausedUpdates = updates.length;
  advance(3_000);
  assert.equal(updates.length, pausedUpdates);
  changeState('active');
  assert.equal(updates.at(-1), time, 'resume refreshes synchronously');
  assert.equal(primary - updates.at(-1), 7_000);
  changeState('active');
  assert.equal(timers.size, 1, 'duplicate active events must not multiply timers');

  const secondSide = time + 15_000;
  render(secondSide);
  assert.equal(timers.size, 1, 'extending a deadline replaces the old timer');
  changeState('background');
  advance(20_000);
  changeState('active');
  assert.equal(updates.at(-1), time);
  assert.ok(updates.at(-1) > secondSide, 'expired deadlines stay expired on resume');
  assert.equal(timers.size, 0);

  render(time + 250);
  advance(100);
  advance(100);
  assert.equal([...timers.values()][0].due - time, 50, 'final tick reaches the deadline');
  advance(50);
  assert.equal(updates.at(-1), time);
  assert.equal(timers.size, 0, 'expiry stops ticking without clearing stored deadlines');

  for (const state of [null, 'unknown', 'inactive', 'background']) {
    changeState(state);
    const count = updates.length;
    render(time + 1_000);
    advance(100);
    assert.equal(timers.size, 0);
    assert.equal(updates.length, count, 'a new deadline cannot start background ticking');
  }
  changeState('active');
  assert.equal(timers.size, 1);
  cleanup();
  assert.equal(timers.size, 0);
  assert.equal(listeners.size, 0, 'unmount removes the subscription');
  const count = updates.length;
  advance(10_000);
  changeState('active');
  assert.equal(updates.length, count);
});
