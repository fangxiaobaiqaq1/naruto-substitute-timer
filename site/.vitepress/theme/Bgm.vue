<script setup>
import { ref, onMounted } from 'vue'

const audio = ref()
const on = ref(false)
const OFF = 'bgm-off' // 用户关过就记住，下次不再自动放

function play() {
  // 首页背景视频在放的话，跳到同一位置，画面和配乐对得上。
  // 必须等开始播放后再跳：preload=none 时提前设置 currentTime 会被忽略。
  const a = audio.value
  a.play()
    .then(() => {
      const v = document.querySelector('.backdrop video')
      if (v && !v.paused) a.currentTime = v.currentTime
    })
    .catch(() => {})
}
const kick = () => {
  disarm()
  play()
}
const disarm = () => document.removeEventListener('click', kick)

function toggle() {
  disarm()
  if (audio.value.paused) {
    localStorage.removeItem(OFF)
    play()
  } else {
    localStorage.setItem(OFF, '1')
    audio.value.pause()
  }
}

// 浏览器不允许打开页面就有声播放：在页面上第一次点击时开始放
onMounted(() => localStorage.getItem(OFF) || document.addEventListener('click', kick))
</script>

<template>
  <audio ref="audio" src="/media/konan.m4a" loop preload="none" @play="on = true" @pause="on = false" />
  <button class="bgm" :class="{ on }" type="button" :aria-pressed="on" aria-label="背景音乐" @click="toggle">
    <span class="bars" aria-hidden="true"><i /><i /><i /><i /></span>
    <span class="label" aria-hidden="true">{{ on ? '关闭音乐' : '播放音乐' }}</span>
  </button>
</template>

<style scoped>
.bgm {
  position: fixed;
  right: 16px;
  bottom: 16px;
  z-index: 20;
  display: flex;
  align-items: center;
  gap: 8px;
  height: 40px;
  padding: 0 16px 0 14px;
  border: 1px solid rgba(255, 255, 255, 0.22);
  border-radius: 999px;
  background: rgba(10, 12, 28, 0.62);
  backdrop-filter: blur(10px);
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  transition: border-color 0.25s, box-shadow 0.25s;
}
.bgm.on {
  border-color: rgba(255, 143, 171, 0.65);
  box-shadow: 0 0 18px rgba(255, 77, 109, 0.4);
}
.bars {
  display: flex;
  align-items: flex-end;
  gap: 2px;
  height: 14px;
}
.bars i {
  width: 3px;
  height: 35%;
  border-radius: 1px;
  background: #ff8fab;
}
.on .bars i {
  animation: eq 0.8s ease-in-out infinite alternate;
}
.on .bars i:nth-child(2) {
  animation-delay: -0.5s;
}
.on .bars i:nth-child(3) {
  animation-delay: -0.2s;
}
.on .bars i:nth-child(4) {
  animation-delay: -0.65s;
}
@keyframes eq {
  to {
    height: 100%;
  }
}
/* 手机上只留圆形图标，少挡住下载按钮 */
@media (max-width: 767px) {
  .bgm {
    width: 44px;
    height: 44px;
    padding: 0;
    justify-content: center;
  }
  .label {
    display: none;
  }
}
@media (prefers-reduced-motion: reduce) {
  .on .bars i {
    animation: none;
    height: 80%;
  }
}
</style>
