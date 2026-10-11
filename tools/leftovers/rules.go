package main

import (
	"os"
	"path/filepath"
	"regexp"
)

// noise are the differences no install makes, each with why: what Windows,
// PowerShell, .NET, git and the runner's own programs write by themselves
// while the install, the session and the uninstall run. Each is as narrow as
// it can be, since a rule that passes over too much passes over a leftover
// too. Nothing whose path names Code Goblins or cfo is ever noise, wherever
// it is.
func noise() []rule {
	local, roaming, profile := os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA"), os.Getenv("USERPROFILE")
	files := func(folder, why string) rule {
		return rule{kind: kindFile, pattern: regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(folder) + `(\\|$)`), why: why}
	}
	keys := func(path, why string) rule {
		return rule{kind: kindRegistryKey, pattern: regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(path) + `(\\|$)`), why: why}
	}
	// Windows provisions its own packaged apps, such as Feedback Hub,
	// Narrator and WSL, for a profile by itself, at times of its own, so one
	// can come during a run. Microsoft's own packages carry its publisher's
	// id, 8wekyb3d8bbwe, in their names.
	const packaged = "Windows' own packaged apps, such as Feedback Hub and WSL, which Windows provisions for a profile by itself"
	classes := regexp.MustCompile(`(?i)^HKCU\\Software\\Classes\\`)
	ownClasses := regexp.MustCompile(`(?i)^HKCU\\Software\\Classes\\(CLSID|AppUserModelId)(\\|$)`)
	return []rule{
		{kind: kindFile, pattern: regexp.MustCompile(`(?i)_8wekyb3d8bbwe`), why: packaged},
		{kind: kindRegistryKey, pattern: regexp.MustCompile(`(?i)_8wekyb3d8bbwe`), why: packaged},
		files(filepath.Join(local, "Microsoft", "WindowsApps"), "the commands of "+packaged+", such as wsl.exe"),
		// Windows registers each packaged app's file types, links and classes
		// as it provisions it, under names of the app's own, such as .all and
		// windowsdefender, and trims its own caches there, such as Local
		// Settings' ShellRefresh, so a key or value under Classes that came,
		// changed or went is passed over. Code Goblins registers nothing there
		// but its notification registration and click handlers, in
		// AppUserModelId and CLSID, so everything in those is compared, and
		// the rules are strict: anything under Classes whose name, or what it
		// held or holds, names Code Goblins or its programs is named, even
		// once it went.
		{kind: kindRegistryKey, pattern: classes, unless: ownClasses, isStrict: true, why: "a file type, link, class or cache of " + packaged},
		{kind: kindRegistryValue, pattern: classes, unless: ownClasses, isStrict: true, why: "a file type, link, class or cache of " + packaged},
		keys(`HKCU\Software\Classes\Extensions\ContractId`, "where Windows registers what its packaged apps add to it"),
		{kind: kindRegistryKey, pattern: regexp.MustCompile(`(?i)^HKCU\\Software\\Classes\\AppX[0-9a-z]{32}(\\|$)`), why: "the file types of " + packaged + ", each named AppX and a hash"},
		{kind: kindRegistryKey, pattern: regexp.MustCompile(`(?i)\\Notifications\\BackgroundCapability\\S-1-15-2-`), why: "what an app container may do in the background, which only Windows' packaged apps are"},
		{kind: kindRegistryKey, pattern: regexp.MustCompile(`(?i)^HKCU\\Software\\Classes\\(\.fh|feedback-hub|insiderhub|windows-feedback)(\\|$)`), why: "the file type and links of Feedback Hub, one of " + packaged},
		{kind: kindRegistryKey, pattern: regexp.MustCompile(`(?i)^HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\App Paths\\(wsl|bash|wslconfig)\.exe(\\|$)`), why: "the commands of WSL, one of " + packaged},
		files(filepath.Join(local, "Temp"), "the temp folder, which the runner's steps and the programs they start write to, where anything of Code Goblins' names it"),
		files(filepath.Join(local, "Microsoft", "Windows", "PowerShell"), "PowerShell's module and startup caches"),
		files(filepath.Join(local, "Microsoft", "Windows", "INetCache"), "Windows' internet cache"),
		files(filepath.Join(local, "Microsoft", "Windows", "INetCookies"), "Windows' internet cookies"),
		files(filepath.Join(local, "Microsoft", "Windows", "WebCache"), "Windows' web cache"),
		files(filepath.Join(local, "Microsoft", "Windows", "Explorer"), "Explorer's icon and thumbnail caches"),
		files(filepath.Join(local, "Microsoft", "Windows", "History"), "Windows' history"),
		files(filepath.Join(local, "Microsoft", "Windows", "Caches"), "Windows' own caches"),
		files(filepath.Join(local, "Microsoft", "Windows", "Notifications"), "Windows' own store of notifications"),
		files(filepath.Join(local, "Microsoft", "Credentials"), "Windows' encrypted store of the credentials it holds for the user, which keeps its file when an entry goes"),
		files(filepath.Join(local, "Microsoft", "TokenBroker"), "Windows' sign-in token cache"),
		files(filepath.Join(local, "Microsoft", "CLR_v4.0"), ".NET's usage logs"),
		files(filepath.Join(local, "Microsoft", "CLR_v4.0_32"), ".NET's usage logs"),
		files(filepath.Join(local, "Packages"), "the state of Windows' own packaged apps, such as winget's logs"),
		files(filepath.Join(local, "D3DSCache"), "the graphics driver's shader cache"),
		files(filepath.Join(local, "CrashDumps"), "Windows' crash dumps"),
		files(filepath.Join(local, "npm-cache"), "npm's own download cache, which npm manages"),
		files(filepath.Join(local, "GitHub CLI"), "the GitHub CLI's own record of this machine, which it writes the first time it runs, for any caller"),
		files(filepath.Join(local, "Mozilla"), "the default browser's own profile, which opening the board in it writes, as opening any page does"),
		files(filepath.Join(roaming, "Mozilla"), "the default browser's own profile, which opening the board in it writes, as opening any page does"),
		files(filepath.Join(roaming, "Microsoft", "Windows", "Recent"), "Windows' list of recent files"),
		files(filepath.Join(roaming, "go", "telemetry"), "the Go toolchain's telemetry, which the runner's own go test writes"),
		files(filepath.Join(roaming, "Microsoft", "Crypto"), "Windows' own keys for the credentials it encrypts"),
		files(filepath.Join(roaming, "Microsoft", "Protect"), "Windows' own keys for the credentials it encrypts"),
		files(filepath.Join(profile, "AppData", "LocalLow", "Microsoft", "CryptnetUrlCache"), "Windows' cache of the certificates it checked"),
		{kind: kindFile, pattern: regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(profile) + `\\ntuser\.(dat|ini|pol)`), why: "the profile's registry hive and its logs"},
		{kind: kindFile, pattern: regexp.MustCompile(`(?i)\\\.git\\FETCH_HEAD$`), why: "git's record of the last fetch, which every fetch rewrites"},
		{kind: kindFile, change: changeLeft, pattern: regexp.MustCompile(`(?i)\\\.git\\objects\\`), why: "objects a goblin's commits added, which no ref reaches once its branch is gone and git removes by itself, and which the uninstall kept in its bundle"},
		keys(`HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer`, "Explorer's own state"),
		keys(`HKCU\Software\Microsoft\Windows\Shell`, "Explorer's folder views"),
		keys(`HKCU\Software\Classes\Local Settings`, "Windows' per-user caches of program names and folder views"),
		keys(`HKCU\Software\Microsoft\Windows NT\CurrentVersion\AppCompatFlags`, "Windows' program compatibility records"),
		keys(`HKCU\Software\Microsoft\Windows NT\CurrentVersion\HostActivityManager`, "Windows' records of the packaged apps it ran, such as winget"),
		keys(`HKCU\Software\Microsoft\Windows\CurrentVersion\CapabilityAccessManager`, "Windows' privacy records of which programs asked for the microphone, the camera or the location"),
		keys(`HKCU\Software\Microsoft\Windows\CurrentVersion\CloudStore`, "Windows' own settings store"),
		keys(`HKCU\Software\Microsoft\Windows\CurrentVersion\Security and Maintenance`, "Windows' own security checks"),
		keys(`HKCU\Software\Microsoft\Windows\CurrentVersion\RunNotification`, "Windows' note that it told the user a program starts at login"),
		keys(`HKCU\Software\Microsoft\Windows\CurrentVersion\Notifications\Settings\Windows.SystemToast.StartupApp`, "Windows' own notification that a program starts at login"),
		{kind: kindRegistryValue, pattern: regexp.MustCompile(`(?i)^HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Notifications\\Settings\\Windows\.SystemToast\.StartupApp\\`), why: "Windows' own notification that a program starts at login"},
		{kind: kindRegistryKey, change: changeLeft, pattern: regexp.MustCompile(`(?i)^HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run$`), why: "the key where Windows keeps what starts at login, which every profile comes to have, while each entry in it is compared"},
		{kind: kindRegistryKey, change: changeLeft, pattern: regexp.MustCompile(`(?i)^HKCU\\(Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall|Software\\Classes\\AppUserModelId|Software\\Classes\\CLSID|Software\\Microsoft\\Windows\\CurrentVersion\\Notifications\\Settings|Control Panel\\NotifyIconSettings)$`), why: "a key where Windows lists installed apps, notification senders, classes or tray icons, which every profile comes to have and which the uninstall leaves, since a profile may have held it empty before, while each entry in it is compared"},
		keys(`HKCU\Software\Microsoft\input`, "Windows' typing records"),
		keys(`HKCU\Software\Microsoft\Windows\CurrentVersion\StorageSense`, "Windows' Storage Sense, which measures the drives by itself"),
		keys(`HKCU\Software\Microsoft\Windows\CurrentVersion\Census`, "Windows' device census, which a scheduled task of Windows' own takes on its own schedule"),
		{kind: kindRegistryValue, pattern: regexp.MustCompile(`(?i)^HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Notifications\\Settings\\QuietHours`), why: "Windows' own record of its quiet hours"},
		keys(`HKCU\Software\Microsoft\IdentityCRL`, "Windows' sign-in records"),
		keys(`HKCU\Software\Microsoft\EdgeWebView`, "the WebView2 runtime's own state, which every program that shows a web page writes"),
		keys(`HKCU\Software\Microsoft\EdgeUpdate`, "the WebView2 runtime's own update state"),
		keys(`HKCU\Software\Mozilla`, "the default browser's own settings, which opening the board in it writes, as opening any page does"),
		{kind: kindRegistryValue, pattern: regexp.MustCompile(`(?i)^(HKCU\\Environment|HKLM\\SYSTEM\\CurrentControlSet\\Control\\Session Manager\\Environment)\\Path$`), why: "the PATH, which is compared entry by entry"},
		{kind: kindProcess, change: changeGone, pattern: regexp.MustCompile(`.`), why: "a process that ended is nothing left behind"},
		{kind: kindProcess, change: changeLeft, pattern: regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(os.Getenv("SystemRoot")) + `\\`), why: "a program of Windows, which starts its own on demand"},
		{kind: kindProcess, change: changeLeft, pattern: regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(filepath.Join(os.Getenv("ProgramData"), "Microsoft", "Windows Defender")) + `\\`), why: "a program of Microsoft Defender's, such as MpCmdRun.exe, which Defender starts on its own schedule"},
		{kind: kindProcess, change: changeLeft, pattern: regexp.MustCompile(`^[^\\]+$`), why: "a program this user may not read, which only Windows' own protected services are, and never a program of the user's, Code Goblins' among them"},
		{kind: kindProcess, change: changeLeft, pattern: regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(filepath.Join(os.Getenv("ProgramFiles"), "Mozilla Firefox")) + `\\`), why: "the default browser, the person's own, which the board opened in and which they close themselves"},
		{kind: kindTask, pattern: regexp.MustCompile(`(?i)^\\Microsoft\\`), why: "a task of Windows' own"},
	}
}

// excepted reports a difference no rule may pass over: anything whose path,
// or what it held or holds, such as a registry value naming a program, names
// Code Goblins or its programs. A thing that went is passed over by its rule
// all the same, unless the rule is strict.
var excepted = regexp.MustCompile(`(?i)goblin|cfo`)
