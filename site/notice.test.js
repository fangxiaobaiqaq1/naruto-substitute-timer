// node notice.test.js — 公告接口的格式检查，部署前跑（npm test 会一起跑）。
// 规则与 docs/notice-api.md 一致：软件端会按同样的规则丢弃不合格的条目。
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

export const LIMITS = { notices: 5, id: 64, title: 20, body: 300, hint: 200, links: 3, label: 20 }
export const HOSTS = ['github.com', 'gitee.com', 'hy.312166.xyz']
const LEVELS = ['info', 'warning', 'critical']
const PLATFORMS = ['windows', 'android']
const VERSION = /^$|^v?\d+\.\d+\.\d+$/

export function checkNotice(n) {
  assert.equal(typeof n.id, 'string'); assert.match(n.id, /^[a-z0-9-]+$/); assert.ok(n.id.length <= LIMITS.id)
  assert.ok(LEVELS.includes(n.level), `level ${n.level}`)
  for (const k of ['title', 'body']) assert.ok(typeof n[k] === 'string' && n[k].trim() && n[k].length <= LIMITS[k], k)
  assert.ok(n.hint === undefined || (typeof n.hint === 'string' && n.hint.length <= LIMITS.hint), 'hint')
  assert.ok(Array.isArray(n.links) && n.links.length <= LIMITS.links, 'links')
  for (const l of n.links) {
    assert.ok(typeof l.label === 'string' && l.label && l.label.length <= LIMITS.label, 'link label')
    const u = new URL(l.url)
    assert.ok(u.protocol === 'https:' && HOSTS.includes(u.host), `link host ${l.url}`)
  }
  assert.ok(Array.isArray(n.platforms) && n.platforms.length && n.platforms.every(p => PLATFORMS.includes(p)), 'platforms')
  for (const k of ['minVersion', 'maxVersion']) assert.match(n[k] ?? '', VERSION, k)
  for (const k of ['startAt', 'endAt']) assert.ok(!n[k] || !Number.isNaN(Date.parse(n[k])), k)
  assert.equal(typeof n.everyLaunch, 'boolean')
}

export function checkFeed(feed) {
  assert.equal(feed.schema, 1)
  assert.ok(!Number.isNaN(Date.parse(feed.updatedAt)), 'updatedAt')
  assert.ok(Array.isArray(feed.notices) && feed.notices.length <= LIMITS.notices, 'notices')
  const ids = new Set()
  for (const n of feed.notices) { checkNotice(n); assert.ok(!ids.has(n.id), `duplicate id ${n.id}`); ids.add(n.id) }
}

const feed = JSON.parse(readFileSync(new URL('./public/api/notice.json', import.meta.url), 'utf8'))
checkFeed(feed)
// 反例：链接到其它网站、非 https、空正文都必须被拒绝。
const base = feed.notices[0]
for (const bad of [
  { ...base, links: [{ label: 'x', url: 'https://example.com/' }] },
  { ...base, links: [{ label: 'x', url: 'http://github.com/' }] },
  { ...base, body: ' ' },
  { ...base, level: 'urgent' },
  { ...base, platforms: ['ios'] },
]) assert.throws(() => checkNotice(bad))
console.log(`notice feed ok: ${feed.notices.length} notice(s)`)
