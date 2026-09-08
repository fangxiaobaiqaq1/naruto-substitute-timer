package win

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// Window 描述一个顶层窗口。
type Window struct {
	HWND        uintptr
	PID         uint32
	ProcessName string // 可执行文件名，如 "MuMuPlayer.exe"
	Title       string // 窗口标题
	ClassName   string // 窗口类名
	Visible     bool
}

// muMuProcessNames 是 MuMu 模拟器相关的进程名（小写比较）。
var muMuProcessNames = []string{
	"mumuplayer.exe",      // MuMu 12 主进程
	"nemuplayer.exe",      // MuMu 6/旧版主进程
	"mumuvmmheadless.exe", // MuMu 12 虚拟机进程
	"nemuheadless.exe",    // MuMu 6 虚拟机进程
	"nemuservice.exe",     // MuMu 服务进程
	"mumunxmain.exe",      // MuMu NX（新版本主进程）
	"mumunxdevice.exe",    // MuMu NX 设备窗口进程
	"mumu.exe",            // 兜底
}

// ListWindows 枚举所有顶层窗口（含不可见的，供调试用）。
func ListWindows() []Window {
	var out []Window
	enumWindows(func(hwnd uintptr, _ uintptr) uintptr {
		w := Window{
			HWND:      hwnd,
			PID:       getWindowThreadProcessID(hwnd),
			Title:     getWindowText(hwnd),
			ClassName: getClassName(hwnd),
			Visible:   isWindowVisible(hwnd),
		}
		if w.PID != 0 {
			w.ProcessName = processName(w.PID)
		}
		out = append(out, w)
		return 1 // 继续枚举
	})
	sort.Slice(out, func(i, j int) bool {
		return out[i].PID < out[j].PID
	})
	return out
}

// isMuMuProcess 判断进程名是否属于 MuMu 模拟器（大小写不敏感）。
func isMuMuProcess(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, p := range muMuProcessNames {
		if n == p {
			return true
		}
	}
	return false
}

// findMuMuCache 缓存 FindMuMu 结果。MuMu 窗口句柄长期不变，
// 而每次全系统 EnumWindows + 同步 GetWindowTextW 很贵（曾实测卡 15-20 秒：
// 回调对某个忙窗口的 WM_GETTEXT 同步等待，还连累定时器消息）。
// 缓存后每轮采集只需 IsWindow 校验（快速、无跨进程消息）。
var findMuMuCache = struct {
	sync.Mutex
	wins    []Window
	until   time.Time
}{}

// InvalidateMuMu 丢掉窗口缓存。模拟器重启、截屏失败后必须重找，
// 否则会一直抓已经关掉的旧 HWND。
func InvalidateMuMu() {
	c := &findMuMuCache
	c.Lock()
	c.wins = nil
	c.until = time.Time{}
	c.Unlock()
}

// FindMuMu 查找 MuMu 模拟器主窗口。
// 缓存最多 1.5 秒：太长会错过「关掉再开」的新窗口，太短会每帧全系统枚举。
func FindMuMu() []Window {
	c := &findMuMuCache
	c.Lock()
	defer c.Unlock()

	now := time.Now()
	if now.Before(c.until) && len(c.wins) > 0 {
		valid := true
		for _, w := range c.wins {
			if !IsWindow(w.HWND) {
				valid = false
				break
			}
		}
		if valid {
			return c.wins
		}
	}

	c.wins = findMuMuWindows()
	c.until = now.Add(1500 * time.Millisecond)
	return c.wins
}

// findMuMuWindows 实际枚举查找（结果不缓存）。
func findMuMuWindows() []Window {
	if wins := findMuMuByProcess(); len(wins) > 0 {
		return wins
	}
	return findMuMuByTitle()
}

// findMuMuByProcess 快路径：只按进程名匹配。
// 枚举回调只取 PID（GetWindowThreadProcessId 是快速 API，无跨进程消息）；
// 每个 PID 只 OpenProcess 一次取进程名；命中 MuMu 进程的窗口才同步取标题/类名。
func findMuMuByProcess() []Window {
	type wi struct {
		hwnd uintptr
		pid  uint32
		vis  bool
	}
	var wins []wi
	names := map[uint32]string{}
	enumWindows(func(hwnd uintptr, _ uintptr) uintptr {
		pid := getWindowThreadProcessID(hwnd)
		if pid != 0 {
			if _, ok := names[pid]; !ok {
				names[pid] = processName(pid)
			}
		}
		wins = append(wins, wi{hwnd: hwnd, pid: pid, vis: isWindowVisible(hwnd)})
		return 1 // 继续枚举
	})

	var visible, invisible []Window
	for _, w := range wins {
		if !isMuMuProcess(names[w.pid]) {
			continue
		}
		win := Window{
			HWND:        w.hwnd,
			PID:         w.pid,
			ProcessName: names[w.pid],
			Title:       getWindowText(w.hwnd),
			ClassName:   getClassName(w.hwnd),
			Visible:     w.vis,
		}
		if w.vis {
			visible = append(visible, win)
		} else {
			invisible = append(invisible, win)
		}
	}
	sortMuMu(visible)
	sortMuMu(invisible)
	if len(visible) > 0 {
		return visible
	}
	return invisible
}

// findMuMuByTitle 慢路径：标题包含 "mumu" 兜底（进程名被改名的场景）。
func findMuMuByTitle() []Window {
	all := ListWindows()
	var visible, invisible []Window
	for _, w := range all {
		titleHit := strings.Contains(strings.ToLower(w.Title), "mumu")
		if !titleHit {
			continue
		}
		if w.Visible {
			visible = append(visible, w)
		} else {
			invisible = append(invisible, w)
		}
	}
	sortMuMu(visible)
	sortMuMu(invisible)
	if len(visible) > 0 {
		return visible
	}
	return invisible
}

// sortMuMu 按"设备进程优先 → 面积降序"排序。
func sortMuMu(wins []Window) {
	sort.SliceStable(wins, func(i, j int) bool {
		di, dj := devicePriority(wins[i].ProcessName), devicePriority(wins[j].ProcessName)
		if di != dj {
			return di < dj
		}
		ri, errI := ClientScreenRect(wins[i].HWND)
		rj, errJ := ClientScreenRect(wins[j].HWND)
		if errI != nil {
			return false
		}
		if errJ != nil {
			return true
		}
		return ri.Width()*ri.Height() > rj.Width()*rj.Height()
	})
}

// devicePriority 返回进程的设备优先级（越小越优先）。
// 游戏画面渲染在设备进程的窗口里，必须优先于外壳/工具进程。
func devicePriority(name string) int {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "mumunxdevice.exe", "mumuplayer.exe", "nemuplayer.exe", "mumuvmmheadless.exe", "nemuheadless.exe":
		return 0 // 设备/游戏进程
	case "mumunxmain.exe", "nemuservice.exe", "mumu.exe":
		return 1 // 外壳/服务进程
	default:
		return 2
	}
}

// RestoreWindow 若窗口最小化则恢复为正常大小。
// 注意：绝不调用 SetForegroundWindow —— 抢焦点会打断用户对局操作，
// 导致 MuMu 窗口线程忙、PrintWindow 卡顿（曾因此"前端太卡"）。
func RestoreWindow(hwnd uintptr) error {
	if IsIconic(hwnd) {
		// SW_SHOWNOACTIVATE=4：恢复显示但不激活，不抢焦点
		r, _, _ := user32ShowWindow.Call(hwnd, 4)
		if r == 0 {
			return errors.New("窗口最小化，恢复失败")
		}
	}
	return nil
}

// IsIconic 判断窗口是否最小化（WS_MINIMIZE 样式）。
func IsIconic(hwnd uintptr) bool {
	nIndex := int32(gwlStyle)
	style, _, _ := user32GetWindowLong.Call(hwnd, uintptr(nIndex))
	const wsMinimize = 0x20000000
	return style&wsMinimize != 0
}

// sortByClientArea 按客户区面积降序排序（面积大的视为主窗口）。
func sortByClientArea(wins []Window) {
	sort.SliceStable(wins, func(i, j int) bool {
		ri, errI := ClientScreenRect(wins[i].HWND)
		rj, errJ := ClientScreenRect(wins[j].HWND)
		if errI != nil {
			return false
		}
		if errJ != nil {
			return true
		}
		return ri.Width()*ri.Height() > rj.Width()*rj.Height()
	})
}
