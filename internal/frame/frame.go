// Package frame 定义 UI 层共享的数据帧类型与组装逻辑。
//
// 组装层：把 internal/win（窗口查找、截屏）与检测引擎（engine.Engine）
// 粘合成 UI 可直接消费的 Frame。判色/识别全部委托给注入的 Engine，
// frame 不再直接调 detect 判色 —— 引擎可热插拔（RGB / 模型）。
package frame

import (
	"fmt"
	"image"
	"strings"
	"time"

	"narutotimer/internal/detect"
	"narutotimer/internal/engine"
	"narutotimer/internal/win"
)

// Bead 是 UI 只关心的豆子显示信息（坐标已换算为截图像素）。
type Bead struct {
	X, Y    int
	Label   string  // 如 "L1"、"R3"
	Lit     bool    // true = 亮蓝可用
	Dark    bool    // true = 暗色（冷却中）
	Gold    bool    // 金色外观；是否可用由 Lit/Dark 表达。
	Unknown bool    // true = 没看清，不能当暗豆、不能拿来掉 1
	Conf    float64 // 0~1
}

// Frame 是一次刷新得到的完整显示数据。
type Frame struct {
	TextStatus          string
	TextError           string
	Img                 *image.RGBA // 原始截图（不带框，UI 自己画框）
	Beads               []Bead      // 豆位列表（可空）
	Status              string      // 状态栏文本
	Fighting            bool
	Engine              string
	LayoutProfile       string
	Hold                bool
	Scene               string
	LeftNinja           string
	RightNinja          string
	LeftNinjaCandidate  string
	RightNinjaCandidate string
	LeftSlots           int
	RightSlots          int
	PlayerSide          string
	PlayerName          string
	OppName             string
	CapturedAt          time.Time // Successful image acquisition only; zero on failed capture.
	CaptureStarted      time.Time // API request time, not a game event timestamp.
	AnalysisStarted     time.Time // Engine entry, after image validation; zero if analysis was skipped.
	AnalyzedAt          time.Time
	CaptureMethod       string
	Sequence            uint64
	Duplicate           bool // Identical pixels; never count as an independent confirmation.
	Err                 error
}

// Provider 每次调用返回最新一帧。由 UI 定时/事件触发调用。
type Provider func() Frame

// Snapshotter 用注入的引擎采集一帧。通过 NewSnapshotter 构造，
// 返回的函数可直接作为 frame.Provider 使用。
func NewSnapshotter(eng engine.Engine, mode detect.ContentMode) Provider {
	return func() Frame {
		return snapshot(eng, mode)
	}
}

func snapshot(eng engine.Engine, mode detect.ContentMode) Frame {
	var f Frame
	f.CaptureStarted = time.Now()
	f.CaptureMethod = "printwindow-fullcontent"

	wins := win.FindMuMu()
	if len(wins) == 0 {
		win.InvalidateMuMu()
		f.Err = fmt.Errorf("未找到模拟器窗口，正在重试…")
		f.Hold = true
		return f
	}
	w := pickGameWindow(wins)
	img, err := win.CaptureClient(w.HWND)
	if err != nil {
		f.Hold = true
		if win.IsBusyFrame(err) {
			f.Status = "模拟器窗口在动，沿用上次结果"
			return f
		}
		win.InvalidateMuMu()
		f.Err = fmt.Errorf("抓取模拟器失败: %v", err)
		return f
	}
	if img == nil {
		f.Hold = true
		f.Err = fmt.Errorf("抓取模拟器返回空图像")
		return f
	}
	f.CapturedAt = time.Now()
	f.Img = img

	wd, ht := img.Bounds().Dx(), img.Bounds().Dy()
	if wd < 400 || ht < 250 {
		win.InvalidateMuMu()
		f.Status = fmt.Sprintf("已找到 %s，但客户区 %dx%d 过小，重试中", w.Title, wd, ht)
		f.Hold = true
		return f
	}

	if detect.ClassifyScreen(img, mode) == detect.ScreenBlank {
		f.Hold = true
		f.Status = fmt.Sprintf("已连接 %s · 空帧，沿用上次", w.Title)
		return f
	}
	f.AnalysisStarted = time.Now()
	res := eng.Analyze(img)
	f.AnalyzedAt = time.Now()
	fillFromEngine(&f, w, img, res)
	f.Hold = res.Uncertain
	if res.Uncertain && !res.Fighting {
		f.Hold = true
		f.Status = fmt.Sprintf("已连接 %s · 画面切换，沿用上次", w.Title)
	}
	return f
}

func pickGameWindow(wins []win.Window) win.Window {
	for _, w := range wins {
		name := strings.ToLower(w.ProcessName)
		if strings.Contains(name, "device") || strings.Contains(name, "player") {
			return w
		}
	}
	return wins[0]
}

func fillFromEngine(f *Frame, w win.Window, img *image.RGBA, res engine.Result) {
	wd, ht := img.Bounds().Dx(), img.Bounds().Dy()
	scr := "其他界面（非对局）"
	if res.Uncertain {
		scr = "看不清"
	} else if res.Fighting {
		scr = "决斗场对局中"
	}
	if res.Scene != "" {
		if res.GateScore > 0 {
			scr = fmt.Sprintf("%s · %s %.2f", scr, res.Scene, res.GateScore)
		} else {
			scr = fmt.Sprintf("%s · %s", scr, res.Scene)
		}
	}
	f.Fighting = res.Fighting
	f.Engine = res.Name
	f.TextStatus, f.TextError = res.TextStatus, res.TextError
	f.LayoutProfile = res.LayoutProfile
	f.Scene = res.Scene
	f.LeftNinja, f.RightNinja = res.LeftNinja, res.RightNinja
	f.LeftNinjaCandidate, f.RightNinjaCandidate = res.LeftNinjaCandidate, res.RightNinjaCandidate
	f.LeftSlots, f.RightSlots = res.LeftSlots, res.RightSlots
	f.PlayerSide = res.PlayerSide
	f.PlayerName = res.PlayerName
	f.OppName = res.OppName
	if res.PlayerName != "" || res.OppName != "" {
		who := strings.TrimSpace(res.PlayerName + " vs " + res.OppName)
		if res.OppName == "" {
			who = res.PlayerName
		}
		if who != "" {
			scr = fmt.Sprintf("%s · %s", scr, who)
		}
	}
	f.Status = fmt.Sprintf("已连接 %s · %dx%d · %s · %s",
		w.Title, wd, ht, scr, res.Name)
	for _, b := range res.Beads {
		unknown := b.Unknown
		f.Beads = append(f.Beads, Bead{
			X:       b.X,
			Y:       b.Y,
			Label:   b.Label,
			Lit:     (b.Lit || b.Gold) && !unknown,
			Dark:    !b.Lit && !b.Gold && !unknown,
			Gold:    b.Gold,
			Unknown: unknown,
			Conf:    b.Conf,
		})
	}
}

// Slots returns independent per-side topology from the detector. Legacy frames
// without metadata retain explicitly configured counts; arbitrary values cannot
// relax the completeness checks used before a drop becomes a timed event.
func (f Frame) Slots(left bool, fallback int) int {
	n := f.RightSlots
	if left {
		n = f.LeftSlots
	}
	if n == 4 || n == 6 {
		return n
	}
	return fallback
}
