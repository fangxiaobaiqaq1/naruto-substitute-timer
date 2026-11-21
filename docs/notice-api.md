# 公告接口

官网提供软件联网读取的公告：

```
GET https://hy.312166.xyz/api/notice.json
```

- 来源文件：`site/public/api/notice.json`。修改后在 `site/` 运行 `npm test && npm run deploy` 发布。
- 这是 Cloudflare 静态资源，不经过 Worker，不占每天 10 万次的 Worker 免费额度。
- 响应头见 `site/public/_headers`：`application/json; charset=utf-8`、`Cache-Control: public, max-age=300`（改完约 5 分钟内生效）、`Access-Control-Allow-Origin: *`。
- 官网 [公告页](https://hy.312166.xyz/notice) 也读这个接口，部署后可以直接在网页上看到效果。

## 格式（schema 1）

```json
{
  "schema": 1,
  "updatedAt": "2026-11-22T12:00:00+08:00",
  "notices": [
    {
      "id": "2026-11-21-resale",
      "level": "warning",
      "title": "公告",
      "body": "近期有闲鱼倒卖本开源软件，作者对售后不负责，本软件永久开源免费。",
      "hint": "请只从下面的官方仓库免费下载，付费购买的版本可能被改动过。",
      "links": [{ "label": "GitHub 仓库", "url": "https://github.com/..." }],
      "platforms": ["windows", "android"],
      "minVersion": "",
      "maxVersion": "",
      "startAt": "",
      "endAt": "",
      "everyLaunch": true
    }
  ]
}
```

| 字段 | 说明 |
|---|---|
| `schema` | 固定为 1。字段含义不兼容地改变时才加 1；软件只认自己支持的版本。 |
| `id` | 小写字母、数字、`-`，≤64 字符，不重复。**改了公告内容就换一个新 id**，软件据此判断“是不是新公告”。 |
| `level` | `info`（青色）/ `warning`（红色）/ `critical`（红色、更醒目）。 |
| `title` / `body` / `hint` | 纯文本，分别 ≤20 / ≤300 / ≤200 字符，`hint` 可省略。不支持 HTML 或 Markdown。 |
| `links` | 最多 3 个，`label` ≤20 字符；`url` 必须是 `https://`，且主机只能是 `github.com`、`gitee.com`、`hy.312166.xyz`。 |
| `platforms` | `windows`、`android` 的非空子集。 |
| `minVersion` / `maxVersion` | 空或 `0.3.6` / `v0.3.6`，包含端点；为空表示不限。 |
| `startAt` / `endAt` | 空或 ISO 8601 时间；在 `[startAt, endAt)` 内显示。 |
| `everyLaunch` | `true`：每次启动都弹；`false`：同一个 `id` 只弹一次。 |

最多 5 条。`site/notice.test.js` 按上面的规则检查（`npm test`），部署前必须通过。

## 软件接入约定（下个版本实现）

- 启动后在后台读取，超时 5 秒，响应上限 64 KB，只走 https，不阻塞计时。
- 读取失败、`schema` 不认识、或没有适用的条目时，退回程序内置的公告（v0.3.5 的防倒卖公告），不报错打扰用户。
- 逐条按上表校验，不合格的条目直接丢弃，其余照常显示；链接主机不在白名单里的不显示按钮。
- 最近一次成功读取的内容缓存在本地，下次离线启动时使用。
- 现有的“启动时显示公告”开关继续生效：关闭后不弹窗，也不联网读取。
