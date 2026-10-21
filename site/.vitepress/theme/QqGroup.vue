<script setup>
import { ref } from 'vue'

const QQ = '1109044204'
const copied = ref(false)

// 点一下复制群号，手机用户不用手抄 10 位数字
async function copy() {
  try {
    await navigator.clipboard.writeText(QQ)
  } catch {
    // 老的 QQ / 微信内置浏览器没有 clipboard API
    const t = Object.assign(document.createElement('textarea'), { value: QQ })
    document.body.append(t)
    t.select()
    const ok = document.execCommand('copy')
    t.remove()
    if (!ok) return // 复制不了：号码就在按钮上，长按也能选
  }
  copied.value = true
  setTimeout(() => (copied.value = false), 2000)
}
</script>

<template>
  <button class="qq" type="button" @click="copy">
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <!-- simple-icons（CC0）-->
      <path d="M21.395 15.035a40 40 0 0 0-.803-2.264l-1.079-2.695c.001-.032.014-.562.014-.836C19.526 4.632 17.351 0 12 0S4.474 4.632 4.474 9.241c0 .274.013.804.014.836l-1.08 2.695a39 39 0 0 0-.802 2.264c-1.021 3.283-.69 4.643-.438 4.673c.54.065 2.103-2.472 2.103-2.472c0 1.469.756 3.387 2.394 4.771c-.612.188-1.363.479-1.845.835c-.434.32-.379.646-.301.778c.343.578 5.883.369 7.482.189c1.6.18 7.14.389 7.483-.189c.078-.132.132-.458-.301-.778c-.483-.356-1.233-.646-1.846-.836c1.637-1.384 2.393-3.302 2.393-4.771c0 0 1.563 2.537 2.103 2.472c.251-.03.581-1.39-.438-4.673" />
    </svg>
    <span class="txt">
      <small>BUG 反馈 QQ 群</small>
      <b>{{ QQ }}</b>
    </span>
    <span class="hint" aria-live="polite">{{ copied ? '已复制 ✓' : '点击复制' }}</span>
  </button>
</template>

<style scoped>
.qq {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  margin-top: 24px;
  padding: 10px 16px 10px 10px;
  border: 1px solid rgba(255, 143, 171, 0.6);
  border-radius: 16px;
  background: rgba(10, 12, 28, 0.55);
  backdrop-filter: blur(10px);
  box-shadow: 0 0 24px rgba(255, 77, 109, 0.35);
  color: #fff;
  text-align: left;
  transition: box-shadow 0.2s, transform 0.2s;
}
.qq:hover {
  transform: translateY(-1px);
  box-shadow: 0 0 34px rgba(255, 77, 109, 0.6);
}
svg {
  flex: none;
  width: 40px;
  height: 40px;
  padding: 7px;
  border-radius: 11px;
  background: #12b7f5; /* QQ 蓝，一眼认出是 QQ */
  fill: #fff;
}
.txt {
  display: flex;
  flex-direction: column;
  line-height: 1.2;
}
small {
  font-size: 13px;
  color: rgba(255, 255, 255, 0.78);
}
b {
  font-size: 26px;
  letter-spacing: 1px;
  font-variant-numeric: tabular-nums;
}
.hint {
  margin-left: 4px;
  padding: 5px 11px;
  border-radius: 999px;
  background: linear-gradient(135deg, #ff4d6d, #c9184a);
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
}
</style>
