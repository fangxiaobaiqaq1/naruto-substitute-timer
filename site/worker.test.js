// node worker.test.js
import assert from 'node:assert/strict'
import { parseRange } from './worker.js'

assert.deepEqual(parseRange('bytes=0-1', 100), [0, 1]) // Safari 第一次探测就发这个
assert.deepEqual(parseRange('bytes=10-', 100), [10, 99])
assert.deepEqual(parseRange('bytes=50-500', 100), [50, 99])
assert.deepEqual(parseRange('bytes=-30', 100), [70, 99])
assert.deepEqual(parseRange('bytes=-300', 100), [0, 99])
assert.equal(parseRange('bytes=100-', 100), 'unsatisfiable')
assert.equal(parseRange('bytes=-0', 100), 'unsatisfiable')
assert.equal(parseRange(null, 100), null)
assert.equal(parseRange('bytes=-', 100), null)
assert.equal(parseRange('bytes=0-1,5-6', 100), null)
console.log('worker range ok')
