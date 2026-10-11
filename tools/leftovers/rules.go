package main

import (
	"os"
	"path/filepath"
	"regexp"
)

// noise are the differences no install makes, each with why: what Windows,
// PowerShell, .NET and the runner's own tools write by themselves while the
// install, the session and the uninstall run. Each is written as narrowly as
// it can be, since a rule that passes over too much passes over a leftover
// too. Anything under a folder of the profile whose name holds "goblin" is
// never noise.
func noise() []rule {
	local, roaming, profile := os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA"), os.Getenv("USERPROFILE")
	files := func(folder, why string) rule {
		return rule{kind: kindFile, pattern: regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(folder) + `(\\|$)`), why: why}
	}
	keys := func(path, why string) rule {
		return rule{kind: kindRegistryKey, pattern: regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(path) + `(\\|$)`), why: why}
	}
	return []rule{
		{kind: kindFile, pattern: regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(filepath.Join(local, "Temp")) + `\\[^\\]*$`), why: "a file of the temp folder whose name does not hold goblin, which the runner's steps and their tools write"},
		files(filepath.Join(local, "Microsoft", "Windows", "PowerShell"), "PowerShell's module and startup caches"),
		files(filepath.Join(local, "Microsoft", "Windows", "INetCache"), "Windows' internet cache"),
		files(filepath.Join(local, "Microsoft", "Windows", "INetCookies"), "Windows' internet cookies"),
		files(filepath.Join(local, "Microsoft", "Windows", "WebCache"), "Windows' web cache"),
		files(filepath.Join(local, "Microsoft", "Windows", "Explorer"), "Explorer's icon and thumbnail caches"),
		files(filepath.Join(local, "Microsoft", "Windows", "History"), "Windows' history"),
		files(filepath.Join(local, "Microsoft", "Windows", "Notifications"), "Windows' own store of notifications"),
		files(filepath.Join(local, "Microsoft", "CLR_v4.0"), ".NET's usage logs"),
		files(filepath.Join(local, "Microsoft", "CLR_v4.0_32"), ".NET's usage logs"),
		files(filepath.Join(local, "D3DSCache"), "the graphics driver's shader cache"),
		files(filepath.Join(local, "CrashDumps"), "Windows' crash dumps"),
		files(filepath.Join(local, "npm-cache"), "npm's own download cache, which npm manages"),
		files(filepath.Join(roaming, "Microsoft", "Windows", "Recent"), "Windows' list of recent files"),
		{kind: kindFile, pattern: regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(profile) + `\\ntuser\.(dat|ini|pol)`), why: "the profile's registry hive and its logs"},
		keys(`HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer`, "Explorer's own state"),
		keys(`HKCU\Software\Microsoft\Windows\Shell`, "Explorer's folder views"),
		keys(`HKCU\Software\Classes\Local Settings`, "Windows' per-user caches of program names and folder views"),
		keys(`HKCU\Software\Microsoft\Windows NT\CurrentVersion\AppCompatFlags`, "Windows' program compatibility records"),
		{kind: kindProcess, change: changeGone, pattern: regexp.MustCompile(`.`), why: "a process that ended is nothing left behind"},
		{kind: kindProcess, change: changeLeft, pattern: regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(os.Getenv("SystemRoot")) + `\\`), why: "a program of Windows, which starts its own on demand"},
		{kind: kindTask, pattern: regexp.MustCompile(`(?i)^\\Microsoft\\`), why: "a task of Windows' own"},
	}
}

// excepted reports a difference no rule may pass over: anything whose path
// names Code Goblins.
var excepted = regexp.MustCompile(`(?i)goblin`)
