# 秽土柱间 HUD 回归

两张 2026-11-14 收到的用户截图（原图 1266×712）的顶部 `[0,0,1266,112)`，**原像素、不缩放**，只含游戏 HUD，不含账号。完整原图保留在本机 `inbox/regressions/hashirama-edo-20261114/`；来源哈希见 `internal/ninja/templates/hashirama_edo_camp.provenance.json`。

| 图 | 左侧 | 右侧 |
|---|---|---|
| red.png | 红色 5/6，最后一格空 | 红色 6/6 |
| blue-three.png | 蓝色 3/6，后三格空 | 红色 6/6 |

`TestHashiramaEdoSixSlotUserHUD` 将 HUD 放回 1266×712 黑底，测试原尺寸与 960/1280/1920 显式缩放、重复采样和去掉身份的反例。测试指定 camp 布局；黑底不用于证明场景门控，两个静态截图也不代表真实替身序列或实测延迟。

```sh
go test ./internal/ninja ./internal/engine/rgb -run 'TestHashiramaEdo|TestReviewedTargetTitles' -count=1
```
