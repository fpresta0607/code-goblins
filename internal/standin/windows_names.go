package standin

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// windowsPrograms are programs Windows ships that malware runs or hides as,
// without their extension. A file with one of these names outside Windows'
// own folders is what Microsoft Defender reads as a program hiding as part of
// Windows. The list is the same on every machine, so a name it holds is
// refused on a GitHub runner as it is on a PC; isWindowsProgram also refuses
// whatever else the Windows it runs on ships.
var windowsPrograms = []string{
	"at", "attrib", "bash", "bitsadmin", "certutil", "cmd", "cmdkey", "conhost", "control", "cscript", "csrss", "ctfmon",
	"curl", "dllhost", "explorer", "find", "findstr", "forfiles", "fsutil", "icacls", "lsass", "mmc", "more", "mshta",
	"msiexec", "net", "net1", "netsh", "notepad", "ping", "powershell", "powershell_ise", "reg", "regedit", "regsvr32",
	"robocopy", "runas", "rundll32", "sc", "schtasks", "services", "sihost", "smss", "sort", "spoolsv", "svchost", "tar",
	"taskhostw", "taskkill", "tasklist", "taskmgr", "timeout", "where", "whoami", "wininit", "winlogon", "wmic", "wscript",
	"wsl", "xcopy",
}

// programExtensions are the endings Windows starts a file by.
var programExtensions = []string{".exe", ".com", ".cmd", ".bat"}

// isWindowsProgram reports whether name, a file's base name, is that of a
// program Windows ships, whatever its case and whichever program extension it
// has: rundll32.exe, CMD.EXE and where.cmd all are.
func isWindowsProgram(name string) bool {
	name = strings.ToLower(name)
	extension := filepath.Ext(name)
	if !slices.Contains(programExtensions, extension) {
		return false
	}
	bare := strings.TrimSuffix(name, extension)
	if slices.Contains(windowsPrograms, bare) {
		return true
	}
	if runtime.GOOS != "windows" {
		return false
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		return false
	}
	for _, folder := range []string{root, filepath.Join(root, "System32"), filepath.Join(root, "System32", "WindowsPowerShell", "v1.0")} {
		for _, shipped := range []string{".exe", ".com"} {
			if _, err := os.Stat(filepath.Join(folder, bare+shipped)); err == nil {
				return true
			}
		}
	}
	return false
}
