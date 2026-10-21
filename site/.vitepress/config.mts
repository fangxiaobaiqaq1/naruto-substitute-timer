import { defineConfig } from 'vitepress'

const gh = 'https://github.com/fangxiaobaiqaq1/naruto-substitute-timer'
const gitee = 'https://gitee.com/xiaobaiqaq/naruto-substitute-timer'

export default defineConfig({
  lang: 'zh-CN',
  title: '替身计时器',
  description: '火影忍者手游替身计时器：自动识别替身豆，悬浮窗显示对方替身倒计时。支持 MuMu、雷电和 Android。',
  cleanUrls: true,
  appearance: 'force-dark', // 配合视频背景，只做深色
  head: [
    ['link', { rel: 'icon', href: "data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>⏱️</text></svg>" }],
  ],
  themeConfig: {
    nav: [
      { text: '下载', link: '/#download' },
      { text: '教程', link: '/guide/usage', activeMatch: '/guide/' },
      { text: '更新日志', link: `${gitee}/releases` },
    ],
    sidebar: {
      '/guide/': [
        {
          text: '教程',
          items: [
            { text: '使用说明', link: '/guide/usage' },
            { text: '诊断说明', link: '/guide/diagnostics' },
          ],
        },
      ],
    },
    socialLinks: [
      { icon: 'github', link: gh },
      { icon: 'gitee', link: gitee },
    ],
    outline: { label: '本页目录' },
    docFooter: { prev: '上一篇', next: '下一篇' },
    sidebarMenuLabel: '菜单',
    returnToTopLabel: '回到顶部',
    skipToContentLabel: '跳到正文',
    notFound: { title: '页面不存在', quote: '这页没找到，回首页看看吧。', linkLabel: '返回首页', linkText: '返回首页' },
  },
})
