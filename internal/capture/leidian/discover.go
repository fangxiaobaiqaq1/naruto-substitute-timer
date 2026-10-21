package leidian

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// installSource says where a possible LDPlayer root was found. Candidates are
// ranked by source, most trustworthy first, instead of lexicographically: the
// former sort let a user-creatable C:\ProgramData\leidian or C:\leidian win
// over a real installation on D:, and ldconsole.exe/adb.exe from the winner
// are executed.
type installSource int

const (
	sourceRunning      installSource = iota // image directory of this user's running dnplayer.exe
	sourceRegistry                          // uninstall entry or LDPlayer's own registry key
	sourceProgramFiles                      // %ProgramFiles%, %ProgramFiles(x86)% (admin-writable only)
	sourceUserLocal                         // %LOCALAPPDATA% (this user's profile)
	sourceDriveRoot                         // <fixed drive>:\leidian\…, kept only when its owner is trusted
)

type installCandidate struct {
	Root   string
	Source installSource
}

func findFile(root string, names ...string) string {
	for _, name := range names {
		candidate := name
		if root != "" && !filepath.IsAbs(candidate) {
			candidate = filepath.Join(root, candidate)
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func consolePath(root string) string { return findFile(root, DefaultConsoleName, "dnconsole.exe") }

// expandInstallCandidates turns discovered locations into LDPlayer roots: each
// location and its LDPlayer* children, kept only when they contain
// ldconsole.exe or dnconsole.exe. Relative or empty locations are ignored.
func expandInstallCandidates(found []installCandidate) []installCandidate {
	var out []installCandidate
	add := func(root string, source installSource) {
		if consolePath(root) != "" {
			out = append(out, installCandidate{Root: filepath.Clean(root), Source: source})
		}
	}
	for _, c := range found {
		root := strings.TrimSpace(c.Root)
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		add(root, c.Source)
		matches, _ := filepath.Glob(filepath.Join(filepath.Clean(root), "LDPlayer*"))
		for _, match := range matches { // Glob returns sorted names
			add(match, c.Source)
		}
	}
	return out
}

// rankInstallCandidates orders roots by source (stable within one source),
// drops the ones trusted rejects and removes case-insensitive duplicates.
func rankInstallCandidates(candidates []installCandidate, trusted func(installCandidate) bool) []string {
	var result []string
	seen := map[string]bool{}
	for source := sourceRunning; source <= sourceDriveRoot; source++ {
		for _, c := range candidates {
			key := strings.ToLower(c.Root)
			if c.Source != source || seen[key] || (trusted != nil && !trusted(c)) {
				continue
			}
			seen[key] = true
			result = append(result, c.Root)
		}
	}
	return result
}

// isLeidianUninstallEntry recognises LDPlayer uninstall entries by key or
// display name (雷电模拟器 / LDPlayer).
func isLeidianUninstallEntry(key, displayName string) bool {
	for _, s := range []string{key, displayName} {
		low := strings.ToLower(s)
		if strings.Contains(low, "ldplayer") || strings.Contains(low, "leidian") || strings.Contains(s, "雷电") {
			return true
		}
	}
	return false
}

// registryPathDir extracts a directory from an uninstall-entry value:
// InstallLocation is the directory itself, DisplayIcon is `"exe",index` and
// UninstallString is a command line whose first token is an executable.
// It returns "" unless the result is an absolute path.
func registryPathDir(value, data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return ""
	}
	var path string
	switch value {
	case "InstallLocation":
		path = strings.Trim(data, `"`)
	default:
		if strings.HasPrefix(data, `"`) {
			end := strings.Index(data[1:], `"`)
			if end < 0 {
				return ""
			}
			path = data[1 : end+1]
		} else if i := strings.Index(strings.ToLower(data), ".exe"); i >= 0 {
			path = data[:i+len(".exe")]
		} else if i := strings.LastIndexByte(data, ','); i >= 0 && strings.IndexFunc(data[i+1:], func(r rune) bool { return !unicode.IsDigit(r) && r != '-' }) < 0 {
			path = data[:i]
		} else {
			path = data
		}
		path = filepath.Dir(strings.TrimSpace(path))
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		return ""
	}
	return path
}
