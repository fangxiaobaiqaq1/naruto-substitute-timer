// timer-gui 是替身计时器的可视化校准工具：实时显示 MuMu 窗口截图，
// 叠加豆位检测框，验证窗口更换 / 尺寸变化时相对坐标算法是否正确。
//
// 组装层：frame.Snapshot（win 截屏 → detect 检测 → Frame）→ gui 显示。
package main

import (
	"fmt"
	"os"

	"narutotimer/internal/engine/factory"
	"narutotimer/internal/frame"
	"narutotimer/internal/gui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "timer-gui:", err)
		os.Exit(1)
	}
}

func run() error {
	eng := factory.MustDefault()
	return gui.Run(frame.NewSnapshotter(eng, factory.DefaultConfig().Mode))
}
