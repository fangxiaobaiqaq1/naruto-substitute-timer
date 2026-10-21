//go:build windows && amd64

package leidian

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// installDirNames are the folder names LDPlayer editions install into.
var installDirNames = []string{"leidian", "LDPlayer", "LDPlayer9", "LDPlayer14"}

// discoverInstallCandidates lists possible LDPlayer locations by source. It
// never scans %ProgramData% or removable/network drives: standard users can
// create folders there (and a disconnected network drive can stall the scan).
func discoverInstallCandidates() []installCandidate {
	var found []installCandidate
	for _, root := range runningPlayerRoots() {
		found = append(found, installCandidate{Root: root, Source: sourceRunning})
	}
	for _, root := range registryInstallRoots() {
		found = append(found, installCandidate{Root: root, Source: sourceRegistry})
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		if base := os.Getenv(env); base != "" {
			for _, name := range installDirNames {
				found = append(found, installCandidate{Root: filepath.Join(base, name), Source: sourceProgramFiles})
			}
		}
	}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		for _, name := range installDirNames {
			found = append(found, installCandidate{Root: filepath.Join(base, name), Source: sourceUserLocal})
		}
	}
	for _, drive := range fixedDriveRoots() {
		found = append(found, installCandidate{Root: drive + "leidian", Source: sourceDriveRoot})
	}
	return found
}

// runningPlayerRoots returns the directories of this user's running
// dnplayer.exe processes. Other users' processes cannot be opened and are
// skipped.
func runningPlayerRoots() []string {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var roots []string
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "dnplayer.exe") {
			continue
		}
		if path := processImagePath(entry.ProcessID); path != "" {
			roots = append(roots, filepath.Dir(path))
		}
	}
	return roots
}

func processImagePath(pid uint32) string {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(process)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(process, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// registryInstallRoots reads LDPlayer's uninstall entries (machine-wide, both
// registry views, and per-user) and LDPlayer's own Software\leidian keys.
func registryInstallRoots() []string {
	var roots []string
	const uninstall = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	for _, location := range []struct {
		hive registry.Key
		view uint32
	}{
		{registry.LOCAL_MACHINE, registry.WOW64_64KEY},
		{registry.LOCAL_MACHINE, registry.WOW64_32KEY},
		{registry.CURRENT_USER, 0},
	} {
		forEachSubKey(location.hive, uninstall, location.view, func(name string, key registry.Key) {
			display, _, _ := key.GetStringValue("DisplayName")
			if !isLeidianUninstallEntry(name, display) {
				return
			}
			for _, value := range []string{"InstallLocation", "DisplayIcon", "UninstallString"} {
				if data, _, err := key.GetStringValue(value); err == nil {
					if dir := registryPathDir(value, data); dir != "" {
						roots = append(roots, dir)
					}
				}
			}
		})
	}
	for _, hive := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
			forEachSubKey(hive, `SOFTWARE\leidian`, view, func(_ string, key registry.Key) {
				if data, _, err := key.GetStringValue("InstallDir"); err == nil {
					if dir := registryPathDir("InstallLocation", data); dir != "" {
						roots = append(roots, dir)
					}
				}
			})
		}
	}
	return roots
}

func forEachSubKey(hive registry.Key, path string, view uint32, visit func(string, registry.Key)) {
	parent, err := registry.OpenKey(hive, path, registry.ENUMERATE_SUB_KEYS|view)
	if err != nil {
		return
	}
	defer parent.Close()
	names, err := parent.ReadSubKeyNames(-1)
	if err != nil {
		return
	}
	for _, name := range names {
		key, err := registry.OpenKey(parent, name, registry.QUERY_VALUE|view)
		if err != nil {
			continue
		}
		visit(name, key)
		key.Close()
	}
}

// fixedDriveRoots lists local fixed drives ("C:\") without touching removable
// media or network shares.
func fixedDriveRoots() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var roots []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		path, err := windows.UTF16PtrFromString(root)
		if err == nil && windows.GetDriveType(path) == windows.DRIVE_FIXED {
			roots = append(roots, root)
		}
	}
	return roots
}

// trustedInstallCandidate accepts drive-root finds only when the folder and its
// console program belong to an administrator, SYSTEM, TrustedInstaller or the
// current user, i.e. not to another standard account that created them.
func trustedInstallCandidate(c installCandidate) bool {
	if c.Source != sourceDriveRoot {
		return true
	}
	console := consolePath(c.Root)
	return console != "" && ownedByTrustedPrincipal(c.Root) && ownedByTrustedPrincipal(console)
}

func ownedByTrustedPrincipal(path string) bool {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return false
	}
	for _, sid := range trustedOwnerSIDs() {
		if owner.Equals(sid) {
			return true
		}
	}
	return false
}

func trustedOwnerSIDs() []*windows.SID {
	var sids []*windows.SID
	for _, kind := range []windows.WELL_KNOWN_SID_TYPE{windows.WinBuiltinAdministratorsSid, windows.WinLocalSystemSid} {
		if sid, err := windows.CreateWellKnownSid(kind); err == nil {
			sids = append(sids, sid)
		}
	}
	// NT SERVICE\TrustedInstaller
	if sid, err := windows.StringToSid("S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"); err == nil {
		sids = append(sids, sid)
	}
	if user, err := windows.GetCurrentProcessToken().GetTokenUser(); err == nil {
		sids = append(sids, user.User.Sid)
	}
	return sids
}
