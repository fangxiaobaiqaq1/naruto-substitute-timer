package updates

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Plan is what PrepareRestart hands to the update helper: a copy of the running
// executable, started with ApplyArgument, that waits for the parent to exit and
// then swaps Target for the verified Incoming file.
type Plan struct {
	Parent   uint32 `json:"parent"`
	Target   string `json:"target"`
	Incoming string `json:"incoming"`
	Hash     string `json:"sha256"`
	Size     int64  `json:"size"`
	Version  string `json:"version"`
}

// checkPlan validates everything in a plan that needs no Windows API.
//
// helper is the helper's own executable, or "" in tests that call apply
// directly. PrepareRestart creates the helper as a byte-for-byte copy of
// Target, so a plan that names any other program as its target (for example a
// plan.json written into the cache by something other than the timer) is
// refused before anything is renamed or started.
func checkPlan(p Plan, helper string) error {
	if !filepath.IsAbs(p.Target) || !filepath.IsAbs(p.Incoming) || !strings.EqualFold(filepath.Ext(p.Target), ".exe") {
		return errors.New("更新路径无效")
	}
	rel, err := filepath.Rel(Directory(), p.Incoming)
	if err != nil || !filepath.IsLocal(rel) {
		return errors.New("更新文件不在缓存目录中")
	}
	if p.Parent == 0 || p.Parent == uint32(os.Getpid()) {
		return errors.New("更新进程信息无效")
	}
	if _, ok := version(p.Version); !ok {
		return errors.New("更新版本无效")
	}
	if err = verifyFile(p.Incoming, p.Hash, p.Size); err != nil {
		return err
	}
	if helper != "" {
		if same, err := sameFileContent(helper, p.Target); err != nil || !same {
			return errors.New("更新目标不是启动本次更新的程序，已取消")
		}
	}
	return nil
}

// sameFileContent compares two files by size and SHA-256 (each read is bounded
// by MaxDownload).
func sameFileContent(a, b string) (bool, error) {
	ia, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if !ia.Mode().IsRegular() || !ib.Mode().IsRegular() || ia.Size() != ib.Size() || ia.Size() > MaxDownload {
		return false, nil
	}
	ha, err := fileSHA256(a)
	if err != nil {
		return false, err
	}
	hb, err := fileSHA256(b)
	if err != nil {
		return false, err
	}
	return ha == hb, nil
}

func fileSHA256(path string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, MaxDownload+1)); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}
