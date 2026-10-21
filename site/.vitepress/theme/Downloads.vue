<script setup>
import { ref, onMounted } from 'vue'

const gh = 'https://github.com/fangxiaobaiqaq1/naruto-substitute-timer/releases'
const gitee = 'https://gitee.com/xiaobaiqaq/naruto-substitute-timer/releases'
const tag = ref('')

// Gitee 没有 GitHub 那种 releases/latest/download/<文件> 跳转，只能先查最新标签。
// 两个接口都允许跨域，直接在用户浏览器里请求。Gitee 匿名接口按 IP 限流（手机网络很多人共用 IP），
// 被限就问 GitHub（两边发布同一个标签）；都查不到时国内按钮退回 Gitee 最新发布页。
onMounted(async () => {
  for (const api of [
    'https://gitee.com/api/v5/repos/xiaobaiqaq/naruto-substitute-timer/releases/latest',
    'https://api.github.com/repos/fangxiaobaiqaq1/naruto-substitute-timer/releases/latest',
  ]) {
    try {
      const t = (await (await fetch(api)).json()).tag_name
      if (/^v\d+\.\d+\.\d+$/.test(t)) return void (tag.value = t)
    } catch {}
  }
})

const cn = (f) => (tag.value ? `${gitee}/download/${tag.value}/${f}` : `${gitee}/latest`)
const intl = (f) => `${gh}/latest/download/${f}`

// 图标来自 simple-icons（CC0）
const icon = {
  windows: 'M0 0h11.377v11.372H0Zm12.623 0H24v11.372H12.623ZM0 12.623h11.377V24H0Zm12.623 0H24V24H12.623',
  android: 'M18.44 5.559q-1.015 1.748-2.028 3.498q-.055-.023-.111-.043a12.1 12.1 0 0 0-8.68.033C7.537 8.897 5.868 6.026 5.6 5.56a1 1 0 0 0-.141-.19a1.104 1.104 0 0 0-1.768 1.298c1.947 3.37-.096-.216 1.948 3.36c.017.03-.495.263-1.393 1.017C2.9 12.176.452 14.772 0 18.99h24a11.7 11.7 0 0 0-.746-3.068a12.1 12.1 0 0 0-2.74-4.184a12 12 0 0 0-2.131-1.687c.66-1.122 1.312-2.256 1.965-3.385a1.11 1.11 0 0 0-.008-1.12a1.1 1.1 0 0 0-.852-.532c-.522-.054-.939.313-1.049.545m-.04 8.46c.395.593.324 1.331-.156 1.65c-.48.32-1.188.1-1.582-.493s-.324-1.33.156-1.65c.473-.316 1.182-.11 1.582.494m-11.193-.492c.48.32.55 1.058.156 1.65c-.394.593-1.103.815-1.584.495c-.48-.32-.55-1.058-.156-1.65c.4-.603 1.109-.811 1.584-.495',
  gitee: 'M11.984 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12a12 12 0 0 0 12-12A12 12 0 0 0 12 0zm6.09 5.333c.328 0 .593.266.592.593v1.482a.594.594 0 0 1-.593.592H9.777c-.982 0-1.778.796-1.778 1.778v5.63c0 .327.266.592.593.592h5.63c.982 0 1.778-.796 1.778-1.778v-.296a.593.593 0 0 0-.592-.593h-4.15a.59.59 0 0 1-.592-.592v-1.482a.593.593 0 0 1 .593-.592h6.815c.327 0 .593.265.593.592v3.408a4 4 0 0 1-4 4H5.926a.593.593 0 0 1-.593-.593V9.778a4.444 4.444 0 0 1 4.445-4.444h8.296Z',
  github: 'M12 .297c-6.63 0-12 5.373-12 12c0 5.303 3.438 9.8 8.205 11.385c.6.113.82-.258.82-.577c0-.285-.01-1.04-.015-2.04c-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729c1.205.084 1.838 1.236 1.838 1.236c1.07 1.835 2.809 1.305 3.495.998c.108-.776.417-1.305.76-1.605c-2.665-.3-5.466-1.332-5.466-5.93c0-1.31.465-2.38 1.235-3.22c-.135-.303-.54-1.523.105-3.176c0 0 1.005-.322 3.3 1.23c.96-.267 1.98-.399 3-.405c1.02.006 2.04.138 3 .405c2.28-1.552 3.285-1.23 3.285-1.23c.645 1.653.24 2.873.12 3.176c.765.84 1.23 1.91 1.23 3.22c0 4.61-2.805 5.625-5.475 5.92c.42.36.81 1.096.81 2.22c0 1.606-.015 2.896-.015 3.286c0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12',
}

const platforms = [
  {
    key: 'windows',
    name: 'Windows 电脑版',
    req: 'Windows 10/11，配合 MuMu 或雷电模拟器',
    file: 'timer-app.exe',
    tip: '提示“已保护你的电脑”？点「更多信息」→「仍要运行」。',
  },
  {
    key: 'android',
    name: 'Android 手机版',
    req: 'Android 12 及以上，手机直接用',
    file: 'naruto-timer-universal.apk',
    tip: '在 QQ / 微信里下载不了？点右上角「…」→「在浏览器打开」。',
  },
]
</script>

<template>
  <section id="download" class="downloads">
    <div class="container">
      <p class="version">
        最新版本
        <a :href="tag ? `${gitee}/tag/${tag}` : `${gitee}/latest`">{{ tag || '查看' }}</a>
        · 免费下载
      </p>

      <div class="grid">
        <div v-for="p in platforms" :key="p.key" :class="['card', p.key]">
          <div class="head">
            <svg class="os" viewBox="0 0 24 24" aria-hidden="true"><path :d="icon[p.key]" /></svg>
            <div>
              <h2>{{ p.name }}</h2>
              <p>{{ p.req }}</p>
            </div>
          </div>

          <a class="btn gitee" :href="cn(p.file)">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path :d="icon.gitee" /></svg>
            <span><b>国内下载</b><small>Gitee · 国内用户点这个</small></span>
          </a>
          <a class="btn github" :href="intl(p.file)">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path :d="icon.github" /></svg>
            <span><b>国外下载</b><small>GitHub · 海外用户</small></span>
          </a>

          <p class="tip">{{ p.tip }}</p>
        </div>
      </div>

      <p class="more">
        <a href="/guide/usage">使用教程</a>
        <a :href="tag ? `${gh}/download/${tag}/naruto-timer-${tag}-windows-x64.zip` : `${gh}/latest`">Windows 完整 ZIP</a>
        <a :href="cn('SHA256SUMS.txt')">SHA256 校验</a>
      </p>
    </div>
  </section>
</template>

<style scoped>
/* 留白与 VitePress 首页的 hero / features 对齐 */
.downloads {
  padding: 0 24px 48px;
}
@media (min-width: 640px) {
  .downloads {
    padding: 0 48px 64px;
  }
}
@media (min-width: 960px) {
  .downloads {
    padding: 0 64px 64px;
  }
}
.container {
  max-width: 1152px;
  margin: 0 auto;
}

.version {
  margin: 0 0 12px;
  font-size: 15px;
  color: rgba(255, 255, 255, 0.85);
  text-shadow: 0 1px 6px rgba(0, 0, 0, 0.7);
}
.version a {
  font-weight: 600;
  color: var(--vp-c-brand-1);
}

.grid {
  display: grid;
  gap: 16px;
}
@media (min-width: 768px) {
  .grid {
    grid-template-columns: 1fr 1fr;
  }
}
/* 手机上先显示安卓 */
@media (max-width: 767px) {
  .android {
    order: -1;
  }
}

.card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 24px;
  border: 1px solid rgba(255, 255, 255, 0.14);
  border-radius: 18px;
  /* 毛玻璃：透出背后的视频 */
  background: rgba(12, 16, 36, 0.55);
  backdrop-filter: blur(14px) saturate(140%);
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.35);
}
.head {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-bottom: 4px;
}
.os {
  flex: none;
  width: 40px;
  height: 40px;
  fill: currentColor;
}
.windows .os {
  color: #0078d4;
}
.android .os {
  color: #3ddc84;
}
.head h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 700;
  line-height: 1.3;
}
.head p {
  margin: 2px 0 0;
  font-size: 14px;
  color: var(--vp-c-text-2);
}

.btn {
  display: flex;
  align-items: center;
  gap: 14px;
  min-height: 64px;
  padding: 12px 20px;
  border-radius: 12px;
  transition: filter 0.15s, transform 0.15s;
}
.btn:hover {
  filter: brightness(1.1);
  transform: translateY(-1px);
}
.btn svg {
  flex: none;
  width: 30px;
  height: 30px;
  fill: currentColor;
}
.btn span {
  display: flex;
  flex-direction: column;
  line-height: 1.35;
}
.btn b {
  font-size: 19px;
}
.btn small {
  font-size: 13px;
  opacity: 0.85;
}
/* 国内：绯红（小南的伞）；国外：纸白（小南的折纸） */
.gitee {
  background: linear-gradient(135deg, #ff4d6d, #c9184a);
  color: #fff;
  box-shadow: 0 6px 22px rgba(255, 77, 109, 0.35);
}
.github {
  background: rgba(255, 255, 255, 0.92);
  color: #141a35;
}

.tip {
  margin: 0;
  font-size: 13px;
  color: rgba(255, 255, 255, 0.7);
}

.more {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 6px 20px;
  margin: 20px 0 0;
  font-size: 14px;
  color: rgba(255, 255, 255, 0.75);
  text-shadow: 0 1px 6px rgba(0, 0, 0, 0.7);
}
.more a {
  color: var(--vp-c-brand-1);
}
</style>
