# 场景模板库

这里只放**筛过的**小图，不是整张截图。

## 已入库

| 文件 | 路标 | 场景 | 来源 |
|---|---|---|---|
| `fight_scene_key.png` | 顶栏「场景 / 键位」（1092 宽） | fight | `debug/capture.png` |
| `fight_scene_key_alt.png` | 同上（1072 宽） | fight | dump |
| `fight_scene_key_alt2.png` | 同上（特效压顶栏） | fight | dump |
| `fight_round.png` | 真决斗场顶栏「第N回」外壳（数字已挖掉） | fight | `debug/duel-now.png` |
| `fight_round_r1.png` | 「第1回」外壳（字更矮） | fight | `debug/duel-r1.png` |
| `lobby_bottom.png` | 大厅底栏：忍者/天赋/装备/通灵/秘卷/装扮 | lobby | `inbox/lobby/` |
| `raw_ban_title.png` | 「禁用忍者选择」 | ban | `inbox/regressions/ban-selection-20260906.png` |
| `raw_ban_unlock.png` | 「解锁禁用」 | ban | 同上 |
| `vs_mark.png` | 选人 VS 小标 | vs | `inbox/reject/加载.png` |
| `vs_mark_alt.png` | 1v1 大 VS | vs | `inbox/reject/忍者加载.png` |
| `queue_search.png` | 「正在寻找旗鼓相当的对手」 | queue | `inbox/reject/选人.png` |
| `pick_lock.png` | 「锁定阵容」 | pick | `inbox/reject/选忍者.png` |
| `result_detail.png` | 左上「忍者详情」 | result | `inbox/result/失败.png` |
| `result_actions.png` | 右上「举报 / 保存比赛 / 战斗详情」 | result | 同上（胜负共用） |

中间「失败 / 完胜」大字带角色身体，不入库。胜负靠同一条顶栏识别。

`inbox/reject/` 里那几张其实不是加载黑屏：两张 VS、一张匹配、一张选阵容。已按真实画面入库。

`lobby_back.png` 已从清单移除：它来自含 MuMu 标题栏的禁用忍者页面，通用「返回」按钮不能证明正在大厅。禁用页样本已裁去标题栏，两个模板均使用游戏内容坐标。

普通页面回归覆盖两张大厅、匹配、选阵容、两种 VS、胜负结算及禁用忍者，在 640×360、800×450、960×540、1072×603、1280×720、1600×900、1920×1080、2560×1440 的缩放样本上检查场景。它验证已有素材的缩放兼容性，不代表覆盖全部游戏页面或实际改变游戏内部布局。

`raw_attack_core.png` 仅在已确认对局后辅助处理回合字样短暂失配，`continuationOnly` 阻止它单独建立场景；还必须有同帧两侧豆区证据。禁用页及按钮搜索区按真实定位保留少量缩放余量，避免每帧在大范围内昂贵滑动。

### 不再只依赖普攻图案（2026-09-07）

`substitute_continuity.png` / `.mask.png` 加入替身按钮作为第二条独立的 `continuationOnly` 证据，阈值0.85。普攻图案可以换成六尾手掌、紫色刀刃或完全不可见；只要替身按钮和同帧双侧豆区仍可信，就能维持已确认的对局，不需要为每种普攻皮肤补模板。

来源为 `F:\计时器\inbox\regressions\camp-both-naruto-right-one-20260907.png` 的 (1034,752)–(1126,846) 原图区域，按1920×1080参考归一。Mask 排除按钮外的活动背景、底部豆数装饰和中央数字区域；完整坐标、散列见 `F:\计时器\assets\templates\substitute_continuity.provenance.json`。

它**不证明新对局开始，不识别对面是否按替身，不直接开启倒计时**。仍需最近已确认的对局/同一布局、双侧当前豆区证据；大厅/VS/结算和超时规则照常生效。800～2560宽缩放、合成变暗/中央遮挡、两种控制同时缺失及普通页面负例通过。合成遮挡不是自然技能动画覆盖率证明。

## 决斗场入口补充（2026-09-06）

- `duel_lobby_ninjutsu.png` / `.mask.png`：入口底栏「忍术对战」，原始 1600×900 截图裁剪框 (328,766)–(466,817)，归一为 166×61 的参考尺寸。
- `duel_lobby_ranked.png` / `.mask.png`：入口底栏「段位赛」，原始裁剪框 (640,768)–(743,817)，归一为 124×59。
- 来源为 `inbox/regressions/duel-lobby-20260906.png`，由 MuMu 官方截图 SDK 采集。两处独立文字控件证明页面，而不是角色、背景或通用返回按钮。Mask 保留文字及其边缘，排除大部分活动背景；阈值均为 0.86。
- 已加入 640～2560 宽缩放、背景变化、删除控件负例及真实战斗负例。它补齐了这类入口，不代表任意未提供的玩法入口都可识别。

## 匹配与空帧规则

NCC 预编译模板统计及 mask 连续区段，不降低阈值、不省略参与匹配的像素；每帧只转换实际搜索 ROI 的灰度，同 ROI 变体共享转换结果。测试与原标量算法逐分数比较，并覆盖非零原点、子图 stride、小 mask 和并发使用。

极暗/极亮的平均值不能单独证明空帧；只对近乎均匀的极值画面快速判空，有明显 HUD 对比度时仍执行模板识别。

## 还缺

- 真·加载黑屏 / 全屏转场（可选）
- 其他玩法入口及未提供的普通页面（当前没有独立模板，不能承诺具体定位）

规则：优先将完整原图交到 `inbox/`。头像、名字、血条、豆子及可变技能图案不作为新场景的独立证据；普攻/替身按钮只允许以上受限的连续性辅助用途。
