import { h } from 'vue'
import DefaultTheme from 'vitepress/theme'
import Backdrop from './Backdrop.vue'
import Bgm from './Bgm.vue'
import Downloads from './Downloads.vue'
import QqGroup from './QqGroup.vue'
import './style.css'

export default {
  extends: DefaultTheme,
  Layout: () =>
    h(DefaultTheme.Layout, null, {
      'home-hero-before': () => h(Backdrop), // 首页：视频背景 + 樱花
      'home-hero-info-after': () => h(QqGroup), // 首页：大标题下面的 QQ 群
      'home-hero-after': () => h(Downloads), // 首页：下载卡片
      'layout-bottom': () => h(Bgm), // 全站：音乐开关，切换页面不中断
    }),
}
