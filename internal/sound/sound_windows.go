//go:build windows

package sound

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winmm          = windows.NewLazySystemDLL("winmm.dll")
	procPlaySoundW = winmm.NewProc("PlaySoundW")
	procMCISendW   = winmm.NewProc("mciSendStringW")
)

const (
	sndAsync     = 0x0001
	sndNoDefault = 0x0002
	sndMemory    = 0x0004
	sndFilename  = 0x00020000
)

func playDefault() error {
	stopMCI()
	// DefaultWAV is a package-level embed, so the buffer outlives the async play.
	r, _, err := procPlaySoundW.Call(uintptr(unsafe.Pointer(&DefaultWAV[0])), 0, sndMemory|sndAsync|sndNoDefault)
	if r == 0 {
		return fmt.Errorf("播放默认音效失败：%v", err)
	}
	return nil
}

func playFile(path string) error {
	if strings.EqualFold(filepath.Ext(path), ".wav") {
		stopMCI()
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		if r, _, err := procPlaySoundW.Call(uintptr(unsafe.Pointer(p)), 0, sndFilename|sndAsync|sndNoDefault); r == 0 {
			return fmt.Errorf("播放音效失败：%v", err)
		}
		return nil
	}
	return runMCI(path)
}

// MCI devices belong to the thread that opened them, so one locked goroutine
// owns the alias and serializes every MP3 request.
type mciRequest struct {
	path string // "" stops
	done chan error
}

var mciQueue = make(chan mciRequest)

func init() {
	go func() {
		runtime.LockOSThread()
		open := false
		for req := range mciQueue {
			if open {
				mci("close nt_warn")
				open = false
			}
			if req.path == "" {
				req.done <- nil
				continue
			}
			if err := mci(`open "` + req.path + `" type mpegvideo alias nt_warn`); err != nil {
				req.done <- err
				continue
			}
			open = true
			req.done <- mci("play nt_warn from 0")
		}
	}()
}

func runMCI(path string) error {
	done := make(chan error, 1)
	mciQueue <- mciRequest{path: path, done: done}
	return <-done
}

func stopMCI() {
	done := make(chan error, 1)
	mciQueue <- mciRequest{done: done}
	<-done
}

func mci(command string) error {
	p, err := windows.UTF16PtrFromString(command)
	if err != nil {
		return err
	}
	if r, _, _ := procMCISendW.Call(uintptr(unsafe.Pointer(p)), 0, 0, 0); r != 0 {
		return fmt.Errorf("播放音效失败（MCI 错误 %d）", r)
	}
	return nil
}
