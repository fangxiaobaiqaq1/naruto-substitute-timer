<script setup>
import { ref, onMounted, onUnmounted } from 'vue'

const video = ref()
const canvas = ref()
let stop = () => {}

onMounted(() => {
  if (matchMedia('(prefers-reduced-motion: reduce)').matches) return // 只显示海报
  video.value.muted = true // 自动播放必须静音；服务端渲染的 HTML 里不一定带 muted
  video.value.play().catch(() => {}) // 被拦（省电模式、部分国产浏览器）就继续显示海报
  stop = sakura(canvas.value)
})
onUnmounted(() => stop())

// 樱花：几十片花瓣，按真实帧间隔移动（高刷屏不会变快），scaleX(cos) 模拟翻转
function sakura(el) {
  const ctx = el.getContext('2d')
  const dpr = Math.min(devicePixelRatio || 1, 2)
  const colors = ['#ffd1dc', '#ffb7c5', '#fff0f5', '#ff9eb5']
  const rand = (a, b) => a + Math.random() * (b - a)
  const petals = []
  let w, h, raf, last = performance.now()

  const spawn = (p, anywhere) =>
    Object.assign(p, {
      x: rand(-0.2 * w, w),
      y: anywhere ? rand(0, h) : rand(-60, -10),
      s: rand(5, 10),
      vy: rand(0.5, 1.2),
      vx: rand(0.1, 0.6),
      a: rand(0, 6.3),
      va: rand(-0.03, 0.03),
      f: rand(0, 6.3),
      vf: rand(0.02, 0.05),
      c: Math.random() < 0.12 ? '#ff4d6d' : colors[Math.floor(rand(0, colors.length))], // 少量绯红，呼应小南的伞
    })

  const resize = () => {
    w = el.clientWidth
    h = el.clientHeight
    el.width = w * dpr
    el.height = h * dpr
    const n = Math.round(Math.min(48, Math.max(16, w / 30)))
    while (petals.length < n) petals.push(spawn({}, true))
    petals.length = n
  }

  const frame = (t) => {
    const k = Math.min((t - last) / 16.7, 3)
    last = t
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, w, h)
    ctx.globalAlpha = 0.85
    for (const p of petals) {
      p.f += p.vf * k
      p.a += p.va * k
      p.y += p.vy * k
      p.x += (p.vx + Math.sin(p.f) * 0.6) * k
      if (p.y > h + 20 || p.x > w + 20) spawn(p)
      const s = p.s
      ctx.save()
      ctx.translate(p.x, p.y)
      ctx.rotate(p.a)
      ctx.scale(Math.cos(p.f), 1)
      ctx.fillStyle = p.c
      ctx.beginPath()
      ctx.moveTo(0, s)
      ctx.bezierCurveTo(s, s * 0.3, s * 0.8, -s * 0.9, s * 0.25, -s)
      ctx.lineTo(0, -s * 0.7) // 花瓣尖上的小缺口
      ctx.lineTo(-s * 0.25, -s)
      ctx.bezierCurveTo(-s * 0.8, -s * 0.9, -s, s * 0.3, 0, s)
      ctx.fill()
      ctx.restore()
    }
    raf = requestAnimationFrame(frame)
  }

  resize()
  addEventListener('resize', resize)
  raf = requestAnimationFrame(frame)
  return () => {
    cancelAnimationFrame(raf)
    removeEventListener('resize', resize)
  }
}
</script>

<template>
  <div class="backdrop" aria-hidden="true">
    <video
      ref="video"
      src="/media/konan.mp4"
      poster="/media/poster.jpg"
      muted
      loop
      playsinline
      webkit-playsinline
      x5-playsinline
      preload="none"
    />
    <div class="scrim" />
    <canvas ref="canvas" />
  </div>
</template>

<style scoped>
.backdrop {
  position: fixed;
  inset: 0;
  z-index: -1;
  overflow: hidden;
  background: #0b0f1f url('/media/poster.jpg') center / cover;
}
video,
canvas,
.scrim {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
}
video {
  object-fit: cover;
}
/* 左侧压暗放文字，右侧留给画面；底部压暗衔接下载卡片 */
.scrim {
  background:
    linear-gradient(90deg, rgba(8, 10, 24, 0.75) 0%, rgba(8, 10, 24, 0.3) 55%, rgba(8, 10, 24, 0.1) 100%),
    linear-gradient(180deg, rgba(8, 10, 24, 0.5) 0%, transparent 22%, transparent 55%, rgba(8, 10, 24, 0.85) 100%);
}
@media (max-width: 767px) {
  .scrim {
    background: linear-gradient(180deg, rgba(8, 10, 24, 0.55), rgba(8, 10, 24, 0.7) 60%, rgba(8, 10, 24, 0.9));
  }
}
canvas {
  pointer-events: none;
}
</style>
