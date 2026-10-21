package updates

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Only remove known files in old, program-owned update directories. Active
// downloads/helpers are left alone, and no recursive deletion is used.
func PruneCache(now time.Time) {
	root := Directory()
	if root == "" {
		return
	}
	entries, e := os.ReadDir(root)
	if e != nil {
		return
	}
	for _, item := range entries {
		if !item.IsDir() || item.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := item.Name()
		// Downloads live in <sha256> (GitHub) or gitee-<sha256> (Gitee, the
		// default source); both hold the same known files.
		hash := isSHA256Hex(name) || (strings.HasPrefix(name, "gitee-") && isSHA256Hex(strings.TrimPrefix(name, "gitee-")))
		helper := strings.HasPrefix(name, "apply-")
		if !hash && !helper {
			continue
		}
		info, e := item.Info()
		if e != nil || now.Sub(info.ModTime()) < 24*time.Hour {
			continue
		}
		dir := filepath.Join(root, name)
		children, e := os.ReadDir(dir)
		if e != nil {
			continue
		}
		for _, child := range children {
			if child.IsDir() || child.Type()&os.ModeSymlink != 0 {
				continue
			}
			file := child.Name()
			known := (hash && (file == "timer-app.exe" || (strings.HasPrefix(file, "download-") && strings.HasSuffix(file, ".part")))) || (helper && (file == "updater.exe" || file == "plan.json"))
			if !known {
				continue
			}
			st, e := child.Info()
			if e != nil || now.Sub(st.ModTime()) < 24*time.Hour {
				continue
			}
			_ = os.Remove(filepath.Join(dir, file))
		}
		_ = os.Remove(dir)
	}
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
