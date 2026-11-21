---
title: 公告
---

<script setup>
import { ref, onMounted } from 'vue'

const notices = ref(null)
const error = ref('')
const platformName = { windows: '电脑版', android: '安卓版' }

onMounted(async () => {
  try {
    const res = await fetch('/api/notice.json', { cache: 'no-cache' })
    if (!res.ok) throw new Error('HTTP ' + res.status)
    const feed = await res.json()
    const now = Date.now()
    notices.value = (feed.notices || []).filter(n =>
      (!n.startAt || Date.parse(n.startAt) <= now) && (!n.endAt || now < Date.parse(n.endAt)))
  } catch (e) {
    error.value = '公告加载失败，请稍后刷新。'
  }
})
</script>

# 公告

<p v-if="error" class="notice-error">{{ error }}</p>
<p v-else-if="notices === null">正在加载…</p>
<p v-else-if="notices.length === 0">目前没有公告。</p>

<div v-for="n in notices || []" :key="n.id" :class="['notice', 'notice-' + n.level]">
  <div class="notice-title">{{ n.title }}</div>
  <p class="notice-body">{{ n.body }}</p>
  <p v-if="n.hint" class="notice-hint">{{ n.hint }}</p>
  <p class="notice-links">
    <a v-for="l in n.links" :key="l.url" :href="l.url" target="_blank" rel="noopener">{{ l.label }}</a>
  </p>
  <p class="notice-meta">适用：{{ n.platforms.map(p => platformName[p] || p).join('、') }}</p>
</div>

软件内的公告也来自这里（接口 `/api/notice.json`）。

<style scoped>
.notice { border: 1px solid #783432; border-left: 4px solid #ff7060; background: #2c161a; border-radius: 10px; padding: 14px 18px; margin: 16px 0; }
.notice-info { border-color: #24485a; border-left-color: #5ce1ff; background: #0d1e29; }
.notice-critical { border-left-width: 6px; }
.notice-title { font-weight: 700; color: #ff7060; font-size: 1.1em; }
.notice-info .notice-title { color: #5ce1ff; }
.notice-body { font-weight: 600; margin: 8px 0; }
.notice-hint, .notice-meta { color: var(--vp-c-text-2); font-size: .9em; margin: 4px 0; }
.notice-links a { margin-right: 16px; }
.notice-error { color: #ff8a80; }
</style>
