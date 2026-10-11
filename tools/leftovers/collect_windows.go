//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// hashedSize is the largest file whose content a snapshot hashes. Larger
// files are compared by being there.
const hashedSize = 1 << 20

// options are what one snapshot reads beyond what every snapshot does.
type options struct {
	// also are more folders to list, such as the person's own checkouts.
	also []string
	// content are more folders and files whose small files are hashed, so a
	// change to one is seen, such as a checkout's .git.
	content []string
}

// collect takes a snapshot of this machine for the current user.
func collect(opts options) (Snapshot, error) {
	snapshot := newSnapshot(time.Now().UTC())
	profile, err := profileFolder("USERPROFILE")
	if err != nil {
		return Snapshot{}, err
	}
	local, err := profileFolder("LOCALAPPDATA")
	if err != nil {
		return Snapshot{}, err
	}
	// The snapshot hashes what a harness or git keeps of its configuration
	// in the profile, which an install edits; the rest is compared by being
	// there.
	content := append([]string{
		filepath.Join(profile, ".claude"), filepath.Join(profile, ".claude.json"),
		filepath.Join(profile, ".codex"), filepath.Join(profile, ".pi"),
		filepath.Join(profile, ".agents"), filepath.Join(profile, ".gitconfig"),
		filepath.Join(profile, ".config"),
	}, opts.content...)
	// The Go toolchain's own caches change with every build the snapshot's
	// caller makes, the snapshot's own included, and Code Goblins writes
	// neither: a goblin's cache variables point into the home.
	skipped := []string{filepath.Join(profile, "go"), filepath.Join(local, "go-build")}
	roots := append([]string{profile}, opts.also...)
	for _, drive := range fixedDrives() {
		roots = append(roots, filepath.Join(drive, "CodeGoblins"), filepath.Join(drive, "DevDrives"))
	}
	for _, root := range roots {
		listFiles(&snapshot, root, content, skipped)
	}
	listRegistry(&snapshot)
	listTasks(&snapshot)
	listCredentials(&snapshot)
	listProcesses(&snapshot)
	listVolumes(&snapshot)
	listVirtualDisks(&snapshot)
	return snapshot, nil
}

// listFiles records every file and folder under root, each link as its
// target without following it, and the hash of each small file under a
// content folder.
func listFiles(snapshot *Snapshot, root string, content, skipped []string) {
	if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
		return
	}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path != root || !errors.Is(err, fs.ErrNotExist) {
				snapshot.Unread = append(snapshot.Unread, path+": "+err.Error())
			}
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		for _, skip := range skipped {
			if within(path, skip) {
				return filepath.SkipDir
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			snapshot.Unread = append(snapshot.Unread, path+": "+err.Error())
			return nil
		}
		if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 || isReparsePoint(info) {
			target, err := os.Readlink(path)
			if err != nil {
				target = "unreadable: " + err.Error()
			}
			snapshot.add(kindFile, path, "link to "+target)
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			snapshot.add(kindFile, path, "folder")
			return nil
		}
		detail := ""
		if info.Size() <= hashedSize && underAny(path, content) {
			detail = hashFile(path)
		}
		snapshot.add(kindFile, path, detail)
		return nil
	})
}

// isReparsePoint reports a junction, which Go reports as an irregular file
// or a folder depending on its version.
func isReparsePoint(info fs.FileInfo) bool {
	data, ok := info.Sys().(*windows.Win32FileAttributeData)
	return ok && data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func underAny(path string, folders []string) bool {
	for _, folder := range folders {
		if within(path, folder) {
			return true
		}
	}
	return false
}

func hashFile(path string) string {
	file, err := fsx.Open(path)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "unreadable: " + err.Error()
	}
	return "sha256 " + hex.EncodeToString(sum.Sum(nil))
}

// fixedDrives are this machine's fixed drives, such as C:.
func fixedDrives() []string {
	var drives []string
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	for index := range 26 {
		if mask&(1<<index) == 0 {
			continue
		}
		drive := string(rune('A'+index)) + ":"
		root, _ := windows.UTF16PtrFromString(drive + `\`)
		if windows.GetDriveType(root) == windows.DRIVE_FIXED {
			drives = append(drives, drive+`\`)
		}
	}
	return drives
}

// watchedKeys are the registry keys whose values the snapshot records, with
// their subkeys': where Windows keeps what starts at login, what is
// installed, the environment, and what a program registers to show
// notifications.
var watchedKeys = []struct {
	root registry.Key
	name string
	path string
}{
	{registry.CURRENT_USER, "HKCU", `Environment`},
	{registry.CURRENT_USER, "HKCU", `Software\Microsoft\Windows\CurrentVersion\Run`},
	{registry.CURRENT_USER, "HKCU", `Software\Microsoft\Windows\CurrentVersion\RunOnce`},
	{registry.CURRENT_USER, "HKCU", `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.CURRENT_USER, "HKCU", `Software\Microsoft\Windows\CurrentVersion\Notifications\Settings`},
	{registry.CURRENT_USER, "HKCU", `Software\Classes\AppUserModelId`},
	{registry.CURRENT_USER, "HKCU", `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`},
	{registry.LOCAL_MACHINE, "HKLM", `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
	{registry.LOCAL_MACHINE, "HKLM", `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.LOCAL_MACHINE, "HKLM", `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	{registry.LOCAL_MACHINE, "HKLM", `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`},
	{registry.LOCAL_MACHINE, "HKLM", `SYSTEM\MountedDevices`},
}

// listRegistry records every key under HKCU\Software, so a key a program
// made anywhere there is seen, and the tray's icon settings, the values of
// the watched keys, and each entry of the user's and the machine's PATH.
func listRegistry(snapshot *Snapshot) {
	listKeys(snapshot, registry.CURRENT_USER, `HKCU`, `Software`)
	listKeys(snapshot, registry.CURRENT_USER, `HKCU`, `Control Panel\NotifyIconSettings`)
	for _, watched := range watchedKeys {
		listValues(snapshot, watched.root, watched.name, watched.path)
	}
	for scope, key := range map[string]struct {
		root registry.Key
		path string
	}{
		"user":    {registry.CURRENT_USER, `Environment`},
		"machine": {registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
	} {
		opened, err := registry.OpenKey(key.root, key.path, registry.QUERY_VALUE)
		if err != nil {
			snapshot.Unread = append(snapshot.Unread, scope+" PATH: "+err.Error())
			continue
		}
		raw, _, err := opened.GetStringValue("Path")
		opened.Close()
		if err != nil && !errors.Is(err, registry.ErrNotExist) {
			snapshot.Unread = append(snapshot.Unread, scope+" PATH: "+err.Error())
			continue
		}
		for _, entry := range strings.Split(raw, ";") {
			if entry = strings.TrimSpace(entry); entry != "" {
				snapshot.add(kindPathEntry, scope+" "+entry, "")
			}
		}
	}
}

func listKeys(snapshot *Snapshot, root registry.Key, rootName, path string) {
	key, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		if !errors.Is(err, registry.ErrNotExist) {
			snapshot.Unread = append(snapshot.Unread, rootName+`\`+path+": "+err.Error())
		}
		return
	}
	names, err := key.ReadSubKeyNames(-1)
	key.Close()
	if err != nil {
		snapshot.Unread = append(snapshot.Unread, rootName+`\`+path+": "+err.Error())
		return
	}
	snapshot.add(kindRegistryKey, rootName+`\`+path, "")
	for _, name := range names {
		listKeys(snapshot, root, rootName, path+`\`+name)
	}
}

func listValues(snapshot *Snapshot, root registry.Key, rootName, path string) {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		if !errors.Is(err, registry.ErrNotExist) {
			snapshot.Unread = append(snapshot.Unread, rootName+`\`+path+": "+err.Error())
		}
		return
	}
	defer key.Close()
	names, err := key.ReadValueNames(-1)
	if err != nil {
		snapshot.Unread = append(snapshot.Unread, rootName+`\`+path+": "+err.Error())
		return
	}
	for _, name := range names {
		snapshot.add(kindRegistryValue, rootName+`\`+path+`\`+name, valueText(key, name))
	}
	subkeys, err := key.ReadSubKeyNames(-1)
	if err != nil {
		snapshot.Unread = append(snapshot.Unread, rootName+`\`+path+": "+err.Error())
		return
	}
	for _, subkey := range subkeys {
		listValues(snapshot, root, rootName, path+`\`+subkey)
	}
}

// valueText is a registry value as text: a string or number as it is, and
// anything else by its hash.
func valueText(key registry.Key, name string) string {
	if text, _, err := key.GetStringValue(name); err == nil {
		return text
	}
	if number, _, err := key.GetIntegerValue(name); err == nil {
		return fmt.Sprint(number)
	}
	if list, _, err := key.GetStringsValue(name); err == nil {
		return strings.Join(list, "|")
	}
	if data, _, err := key.GetBinaryValue(name); err == nil {
		sum := sha256.Sum256(data)
		return "binary sha256 " + hex.EncodeToString(sum[:])
	}
	return "unreadable"
}

// listTasks records every scheduled task by its full name.
func listTasks(snapshot *Snapshot) {
	output, err := execx.Command("schtasks", "/query", "/fo", "csv", "/nh").Output()
	if err != nil {
		snapshot.Unread = append(snapshot.Unread, "scheduled tasks: "+err.Error())
		return
	}
	rows := csv.NewReader(strings.NewReader(string(output)))
	rows.FieldsPerRecord = -1
	records, err := rows.ReadAll()
	if err != nil {
		snapshot.Unread = append(snapshot.Unread, "scheduled tasks: "+err.Error())
		return
	}
	for _, record := range records {
		// schtasks repeats its header before the tasks of each folder.
		if len(record) > 0 && strings.HasPrefix(record[0], `\`) {
			snapshot.add(kindTask, record[0], "")
		}
	}
}

// listCredentials records the target of every credential Windows'
// Credential Manager holds for the user.
func listCredentials(snapshot *Snapshot) {
	output, err := execx.Command("cmdkey", "/list").Output()
	if err != nil {
		snapshot.Unread = append(snapshot.Unread, "credentials: "+err.Error())
		return
	}
	for _, line := range strings.Split(string(output), "\n") {
		if _, target, ok := strings.Cut(strings.TrimSpace(line), "Target: "); ok {
			snapshot.add(kindCredential, strings.TrimSpace(target), "")
		}
	}
}

// listProcesses records the program of every running process but this one.
func listProcesses(snapshot *Snapshot) {
	entries, err := proc.Processes()
	if err != nil {
		snapshot.Unread = append(snapshot.Unread, "processes: "+err.Error())
		return
	}
	for _, entry := range entries {
		if entry.PID == os.Getpid() || entry.PID == 0 {
			continue
		}
		identity, err := proc.Identify(entry.PID, entry.Start)
		image := identity.Image
		if err != nil || image == "" {
			image = entry.ExeBase
		}
		snapshot.add(kindProcess, image, "")
	}
}

// listVolumes records every drive with a letter, by its file system and
// label.
func listVolumes(snapshot *Snapshot) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return
	}
	for index := range 26 {
		if mask&(1<<index) == 0 {
			continue
		}
		drive := string(rune('A'+index)) + `:\`
		root, _ := windows.UTF16PtrFromString(drive)
		label := make([]uint16, windows.MAX_PATH+1)
		system := make([]uint16, windows.MAX_PATH+1)
		detail := "unreadable"
		if windows.GetVolumeInformation(root, &label[0], uint32(len(label)), nil, nil, nil, &system[0], uint32(len(system))) == nil {
			detail = windows.UTF16ToString(system) + " " + windows.UTF16ToString(label)
		}
		snapshot.add(kindVolume, drive, strings.TrimSpace(detail))
	}
}

// listVirtualDisks records the file of every virtual disk attached.
func listVirtualDisks(snapshot *Snapshot) {
	output, err := execx.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"Get-Disk | Where-Object { $_.BusType -eq 'File Backed Virtual' } | ForEach-Object { $_.Location }").Output()
	if err != nil {
		snapshot.Unread = append(snapshot.Unread, "virtual disks: "+err.Error())
		return
	}
	for _, line := range strings.Split(string(output), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			snapshot.add(kindVirtualDisk, line, "")
		}
	}
}
