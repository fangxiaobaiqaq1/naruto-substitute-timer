# OpenCV 左右豆检测器（build tag 隔离）

## 目录结构
```
internal/vision/opencv/
├── engine.go          // Analyze 接口，复用 layout.Transform + domain.Snapshot
├── gate.go            // 严格 fight gate（暂时不做，对局固定 unknown）
├── mask.go            // HSV InRange + dark/light/gold mask
├── morphology.go      // MorphologyEx open/close
├── contours.go        // FindContours / ConnectedComponentsWithStats
├── candidates.go      // 左右各选 4 颗候选（nominal + search ROI）
├── score.go           // 统计 light/dark/gold coverage + 形状置信度
├── analyze.go         // 核心：ROI 裁切 + mask + contour + 候选选择 + Decode
└── diagnostics.go     // 调试信息
```

## 外部依赖
- `//go:build opencv`
- `gocv.io/x/gocv v0.43.0`
- OpenCV 4.13 MinGW UCRT（`F:\opencv\install\x64\mingw\bin`）

## 配置（JSON 可覆盖）
```json
{
  "vision": {
    "opencv": {
      "morphology": {
        "kernel": "rect",
        "ksize": 3,
        "iterations": 2
      },
      "contour": {
        "minArea": 50,
        "maxArea": 800,
        "minWidth": 8,
        "maxWidth": 20,
        "minHeight": 12,
        "maxHeight": 28,
        "minSolidity": 0.75
      },
      "geometry": {
        "yTolerance": 3,
        "spacingTolerance": 0.22,
        "mirrorTolerance": 0.025
      }
    }
  }
}
```

## 算法流程（左右各 4 颗豆）

1. `layout.New` 得 `Transform`（不写像素公式）
2. 对左右两侧：
   - 用 `NormalizedContentToCapture` 映射 search ROI + 4 个 nominal center
   - 裁剪 ROI → Mat → `CvtColor` HSV
   - 多段 `InRange` 做 mask → `MorphologyEx` → `FindContours` / `ConnectedComponentsWithStats`
   - 每侧从候选里选 4 颗可信组合（共线、近等距、镜像）
   - 凑不齐就该侧 `BeadUnknown`（不回退旧采样）
3. 每颗豆在 core ROI 统计 light/dark/gold coverage + 形状得 `BeadScores`
4. 最佳类 < `unknownBelow` 或 margin < `minimumMargin` → `unknown`
5. 每侧 4 颗交给 `sequence.Decode`；拒绝则 `Count == nil`
6. `Screen.State` 固定 `unknown`

## 离线 PoC
```
go run -tags opencv ./cmd/vision-poc --image debug/capture.png
go run -tags opencv ./cmd/vision-poc --image debug/live-current.png
```

## 验收门槛
- 正样本 `debug/capture.png`：左右中心误差 ≤ 3 px，真值豆数正确
- 负样本 `debug/live-current.png`：两侧 `unknown`，无确定亮/暗数
- 默认无 OpenCV 构建仍通过

## 编译环境（F 盘）
```bash
source scripts/opencv-env.sh
go test -tags opencv ./internal/vision/opencv
```

## 后续
- 达标后才接正式 UI / shadow comparison
- 保留旧 RGB 路径（`KindRGB`）
- 不做 YOLO / ONNX
- 不改 `build.bat`
