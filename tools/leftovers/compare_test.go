package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func snapshotOf(things map[string]map[string]string) Snapshot {
	snapshot := newSnapshot(time.Unix(0, 0))
	for kind, identities := range things {
		for identity, detail := range identities {
			snapshot.add(kind, identity, detail)
		}
	}
	return snapshot
}

func TestCompareNamesWhatTheUninstallLeftTookOrChanged(t *testing.T) {
	tests := []struct {
		name          string
		before, after map[string]map[string]string
		kept          []string
		rules         []rule
		want          []string
		wantPassed    int
	}{
		{
			name:   "a file left behind",
			before: map[string]map[string]string{kindFile: {`C:\Users\a\x`: ""}},
			after:  map[string]map[string]string{kindFile: {`C:\Users\a\x`: "", `C:\Users\a\AppData\Local\CodeGoblins\state`: "folder"}},
			want:   []string{`left file C:\Users\a\AppData\Local\CodeGoblins\state folder`},
		},
		{
			name:   "a file of the person's taken",
			before: map[string]map[string]string{kindFile: {`C:\Users\a\.claude\settings.json`: "sha256 1"}},
			after:  map[string]map[string]string{kindFile: {}},
			want:   []string{`gone file C:\Users\a\.claude\settings.json sha256 1`},
		},
		{
			name:   "a file of the person's that holds something else",
			before: map[string]map[string]string{kindFile: {`C:\Users\a\.claude\settings.json`: "sha256 1"}},
			after:  map[string]map[string]string{kindFile: {`C:\Users\a\.claude\settings.json`: "sha256 2"}},
			want:   []string{`changed file C:\Users\a\.claude\settings.json: sha256 2, was sha256 1`},
		},
		{
			name:   "a scheduled task left behind",
			before: map[string]map[string]string{kindTask: {}},
			after:  map[string]map[string]string{kindTask: {`\Code Goblins Dev Drive`: ""}},
			want:   []string{`left scheduled task \Code Goblins Dev Drive`},
		},
		{
			name:       "a folder the uninstall named as kept and what is under it",
			before:     map[string]map[string]string{kindFile: {}},
			after:      map[string]map[string]string{kindFile: {`C:\Users\a\Code Goblins kept`: "folder", `C:\Users\a\code goblins kept\data\backlog.md`: ""}},
			kept:       []string{`C:\Users\a\Code Goblins kept`},
			wantPassed: 2,
		},
		{
			name:       "a folder that holds a kept tool, as npm's folder of a scope does",
			before:     map[string]map[string]string{kindFile: {`C:\npm\node_modules`: "folder"}},
			after:      map[string]map[string]string{kindFile: {`C:\npm\node_modules`: "folder", `C:\npm\node_modules\@openai`: "folder", `C:\npm\node_modules\@openai\codex`: "folder"}},
			kept:       []string{`C:\npm\node_modules\@openai\codex`},
			wantPassed: 2,
		},
		{
			name:       "a folder beside a kept tool",
			before:     map[string]map[string]string{kindFile: {`C:\npm\node_modules`: "folder"}},
			after:      map[string]map[string]string{kindFile: {`C:\npm\node_modules`: "folder", `C:\npm\node_modules\@openai`: "folder", `C:\npm\node_modules\@openai\codex`: "folder", `C:\npm\node_modules\@openai\codex-left`: "folder"}},
			kept:       []string{`C:\npm\node_modules\@openai\codex`},
			want:       []string{`left file C:\npm\node_modules\@openai\codex-left folder`},
			wantPassed: 2,
		},
		{
			name:       "a PATH entry naming a kept tool's folder",
			before:     map[string]map[string]string{kindPathEntry: {}},
			after:      map[string]map[string]string{kindPathEntry: {`user C:\Users\a\.local\bin`: ""}},
			kept:       []string{`C:\Users\a\.local\bin`},
			wantPassed: 1,
		},
		{
			name:       "a kept tool's daemon running from its kept folder, and its task the plan named",
			before:     map[string]map[string]string{kindProcess: {}, kindTask: {}},
			after:      map[string]map[string]string{kindProcess: {`C:\Users\a\AppData\Local\no-mistakes\no-mistakes.exe`: ""}, kindTask: {`\no-mistakes-daemon-db03`: ""}},
			kept:       []string{`C:\Users\a\AppData\Local\no-mistakes`, `\no-mistakes-daemon-db03`},
			wantPassed: 2,
		},
		{
			name:   "a task the plan did not name",
			before: map[string]map[string]string{kindTask: {}},
			after:  map[string]map[string]string{kindTask: {`\no-mistakes-daemon-db03`: ""}},
			kept:   []string{`C:\Users\a\AppData\Local\no-mistakes`},
			want:   []string{`left scheduled task \no-mistakes-daemon-db03`},
		},
		{
			name:   "a folder beside a kept one that only starts with its name",
			before: map[string]map[string]string{kindFile: {}},
			after:  map[string]map[string]string{kindFile: {`C:\Users\a\kept-more`: "folder"}},
			kept:   []string{`C:\Users\a\kept`},
			want:   []string{`left file C:\Users\a\kept-more folder`},
		},
		{
			name:   "a file of the person's under a kept folder that was taken",
			before: map[string]map[string]string{kindFile: {`C:\Users\a\kept\mine.txt`: ""}},
			after:  map[string]map[string]string{kindFile: {}},
			kept:   []string{`C:\Users\a\kept`},
			want:   []string{`gone file C:\Users\a\kept\mine.txt`},
		},
		{
			name:       "noise a rule passes over",
			before:     map[string]map[string]string{kindFile: {}},
			after:      map[string]map[string]string{kindFile: {`C:\Users\a\AppData\Local\D3DSCache\x`: ""}},
			rules:      []rule{{kind: kindFile, pattern: regexp.MustCompile(`(?i)D3DSCache`), why: "shader cache"}},
			wantPassed: 1,
		},
		{
			name:   "a rule of another kind passes over nothing",
			before: map[string]map[string]string{kindTask: {}},
			after:  map[string]map[string]string{kindTask: {`\D3DSCache`: ""}},
			rules:  []rule{{kind: kindFile, pattern: regexp.MustCompile(`(?i)D3DSCache`), why: "shader cache"}},
			want:   []string{`left scheduled task \D3DSCache`},
		},
		{
			name:   "a rule for one change passes over no other",
			before: map[string]map[string]string{kindProcess: {}},
			after:  map[string]map[string]string{kindProcess: {`C:\x\cfo.exe`: ""}},
			rules:  []rule{{kind: kindProcess, change: changeGone, pattern: regexp.MustCompile(`.`), why: "ended"}},
			want:   []string{`left process C:\x\cfo.exe`},
		},
		{
			name:   "no rule passes over a thing that names Code Goblins",
			before: map[string]map[string]string{kindFile: {}},
			after:  map[string]map[string]string{kindFile: {`C:\Users\a\AppData\Local\Temp\CodeGoblinsInstall.log`: ""}},
			rules:  []rule{{kind: kindFile, pattern: regexp.MustCompile(`(?i)\\Temp\\`), why: "temp"}},
			want:   []string{`left file C:\Users\a\AppData\Local\Temp\CodeGoblinsInstall.log`},
		},
		{
			name:   "nothing differs",
			before: map[string]map[string]string{kindFile: {`C:\x`: ""}, kindVolume: {`C:\`: "NTFS"}},
			after:  map[string]map[string]string{kindFile: {`C:\x`: ""}, kindVolume: {`C:\`: "NTFS"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			before, after := snapshotOf(test.before), snapshotOf(test.after)

			// Act
			remaining, over := compare(before, after, test.kept, test.rules)

			// Assert
			var got []string
			for _, d := range remaining {
				got = append(got, d.String())
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("compare left\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(test.want, "\n"))
			}
			if len(over) != test.wantPassed {
				t.Errorf("compare passed over %d differences (%v), want %d", len(over), over, test.wantPassed)
			}
			for _, p := range over {
				if p.Why == "" {
					t.Errorf("%s was passed over with no reason", p.difference)
				}
			}
		})
	}
}

func TestWithinComparesPathsAsWindowsDoes(t *testing.T) {
	tests := []struct {
		path, folder string
		want         bool
	}{
		{`C:\Users\a\kept`, `C:\Users\a\kept`, true},
		{`C:\Users\a\KEPT\x`, `c:\users\a\kept`, true},
		{`C:\Users\a\kept\`, `C:\Users\a\kept`, true},
		{`C:\Users\a\kept-more`, `C:\Users\a\kept`, false},
		{`C:\Users\a`, `C:\Users\a\kept`, false},
		{`D:\CodeGoblins\x`, `D:\`, true},
	}
	for _, test := range tests {
		// Act
		got := within(test.path, test.folder)

		// Assert
		if got != test.want {
			t.Errorf("within(%q, %q) = %v, want %v", test.path, test.folder, got, test.want)
		}
	}
}

// The noise rules are what a test of the uninstall could hide a leftover
// behind, so each says why, and none passes over a thing of Code Goblins'
// own, wherever it is.
func TestNoiseNeverPassesOverCodeGoblins(t *testing.T) {
	// Arrange
	t.Setenv("LOCALAPPDATA", `C:\Users\a\AppData\Local`)
	t.Setenv("APPDATA", `C:\Users\a\AppData\Roaming`)
	t.Setenv("USERPROFILE", `C:\Users\a`)
	t.Setenv("SystemRoot", `C:\Windows`)
	leftovers := []difference{
		{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\AppData\Local\Temp\CodeGoblinsInstall.log`},
		{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\AppData\Local\Temp\CodeGoblinsSetup`},
		{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\AppData\Local\Temp\code-goblins-1234`},
		{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\AppData\Local\Microsoft\Windows\Explorer\goblins-window.png`},
		{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\AppData\Local\Temp\jiti\extensions-cfo-native.291f97c5.mjs`},
		{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\AppData\Local\cfo\conpty\OpenConsole.exe`},
		{Change: changeLeft, Kind: kindProcess, Identity: `C:\Windows\goblins.exe`},
	}

	// Act
	rules := noise()

	// Assert
	for _, r := range rules {
		if r.why == "" {
			t.Errorf("the noise rule %s says no why", r.pattern)
		}
	}
	for _, leftover := range leftovers {
		remaining, _ := compare(snapshotOf(nil), snapshotOf(map[string]map[string]string{leftover.Kind: {leftover.Identity: ""}}), nil, rules)
		if len(remaining) != 1 {
			t.Errorf("the noise rules passed over %s", leftover)
		}
	}
}

// What Windows provisions for its own packaged apps during a run, as it did
// on a clean runner, is passed over, every part of it.
func TestNoisePassesOverWhatWindowsProvisionsForItsPackagedApps(t *testing.T) {
	// Arrange
	t.Setenv("LOCALAPPDATA", `C:\Users\a\AppData\Local`)
	provisioned := []difference{
		{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\AppData\Local\Microsoft\WindowsApps\wsl.exe`},
		{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\AppData\Local\Microsoft\WindowsApps\MicrosoftCorporationII.WindowsSubsystemForLinux_8wekyb3d8bbwe\bash.exe`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\.fh\OpenWithProgids`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\feedback-hub`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\AppXm4fjth7rfv9b2vppk3w1gcpsf4gy9r3n\Shell\open\command`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\Extensions\ContractId\Windows.ShareTarget\PackageId`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\App Paths\wsl.exe`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\AppHost\IndexedDB\Microsoft.WindowsFeedbackHub_8wekyb3d8bbwe`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\Authentication\LogonUI\Notifications\BackgroundCapability\S-1-15-2-577703620-721806600\App.AppXa2g9313bzvs2knz17p3sr2k6j7ftv102.mca`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\PushNotifications\Backup\MicrosoftCorporationII.WindowsSubsystemForLinux_8wekyb3d8bbwe!wsl`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\PushNotifications\Backup\Windows.Defender.MpUxDlp`},
	}
	near := []difference{
		{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\AppData\Local\Microsoft\WindowsApps\goblins.exe`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\AppXCodeGoblins`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\CLSID\{0123}`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\PushNotifications\Backup\Windows.Defender.MpUxDlp.CodeGoblins`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\PushNotifications\Backup\CodeGoblins.Notifier`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\PushNotifications\Backup\cfo`},
		{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\AppData\Local\Microsoft\Windows\WindowsAppsX\x.exe`},
	}

	// Act
	rules := noise()

	// Assert
	for _, difference := range provisioned {
		if remaining, _ := compare(snapshotOf(nil), snapshotOf(map[string]map[string]string{difference.Kind: {difference.Identity: ""}}), nil, rules); len(remaining) != 0 {
			t.Errorf("the noise rules named %s", difference)
		}
	}
	for _, difference := range near {
		if remaining, _ := compare(snapshotOf(nil), snapshotOf(map[string]map[string]string{difference.Kind: {difference.Identity: ""}}), nil, rules); len(remaining) != 1 {
			t.Errorf("the noise rules passed over %s", difference)
		}
	}
}

// A key where Windows lists entries, which the uninstall leaves since the
// profile may have held it empty before, is passed over when it is left,
// while an entry in it, or the key gone, is still named. So is a program of
// Defender's own running at the snapshot.
func TestNoisePassesOverWindowsListsButNotWhatIsInThem(t *testing.T) {
	// Arrange
	t.Setenv("ProgramData", `C:\ProgramData`)
	t.Setenv("SystemRoot", `C:\Windows`)
	cases := []struct {
		difference
		isNamed bool
	}{
		{difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall`}, false},
		{difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Control Panel\NotifyIconSettings`}, false},
		{difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\CLSID`}, false},
		{difference{Change: changeLeft, Kind: kindProcess, Identity: `C:\ProgramData\Microsoft\Windows Defender\Platform\4.18.26080.4-0\MpCmdRun.exe`}, false},
		{difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\CodeGoblins`}, true},
		{difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Control Panel\NotifyIconSettings\1234567890`}, true},
		{difference{Change: changeGone, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall`}, true},
		{difference{Change: changeLeft, Kind: kindProcess, Identity: `C:\ProgramData\Microsoft\Windows Defender Advanced\other.exe`}, true},
	}

	// Act
	rules := noise()

	// Assert
	for _, tc := range cases {
		before, after := snapshotOf(map[string]map[string]string{tc.Kind: {}}), snapshotOf(map[string]map[string]string{tc.Kind: {tc.Identity: ""}})
		if tc.Change == changeGone {
			before, after = after, before
		}
		if remaining, _ := compare(before, after, nil, rules); (len(remaining) == 1) != tc.isNamed {
			t.Errorf("%s: named %v, want named %v", tc.difference, remaining, tc.isNamed)
		}
	}
}

// What Windows' own scheduled tasks write while a run goes on, as its device
// census did on a clean runner, is passed over, and a key beside it is not.
func TestNoisePassesOverWhatWindowsTasksWrite(t *testing.T) {
	cases := []struct {
		identity string
		isNamed  bool
	}{
		{identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\Census`, isNamed: false},
		{identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\Census\Store`, isNamed: false},
		{identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\CensusTaker`, isNamed: true},
	}
	for _, tc := range cases {
		t.Run(tc.identity, func(t *testing.T) {
			// Arrange
			before, after := snapshotOf(map[string]map[string]string{kindRegistryKey: {}}), snapshotOf(map[string]map[string]string{kindRegistryKey: {tc.identity: ""}})

			// Act
			remaining, _ := compare(before, after, nil, noise())

			// Assert
			if (len(remaining) == 1) != tc.isNamed {
				t.Errorf("named %v, want named %v", remaining, tc.isNamed)
			}
		})
	}
}

// The empty key Windows' widgets host, Dsh, creates for the profile at a time
// of its own, which some runners already hold in the snapshot taken before
// the install and others come to hold during the run, is passed over as that
// one key left, and nothing near it is.
func TestNoisePassesOverTheKeyWindowsCreatesForItsWidgetsHost(t *testing.T) {
	cases := []struct {
		name string
		difference
		isNamed bool
	}{
		{"the key left", difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\Dsh`}, false},
		{"a key under it", difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\Dsh\Code`}, true},
		{"a value in it", difference{Change: changeLeft, Kind: kindRegistryValue, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\Dsh\Setting`, After: "1"}, true},
		{"a key beside it", difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\DshOther`}, true},
		{"the key gone", difference{Change: changeGone, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\Dsh`}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			before, after := snapshotOf(map[string]map[string]string{tc.Kind: {}}), snapshotOf(map[string]map[string]string{tc.Kind: {tc.Identity: tc.After}})
			if tc.Change == changeGone {
				before, after = after, before
			}

			// Act
			remaining, _ := compare(before, after, nil, noise())

			// Assert
			if (len(remaining) == 1) != tc.isNamed {
				t.Errorf("%s: named %v, want named %v", tc.difference, remaining, tc.isNamed)
			}
		})
	}
}

// The rule for HKCU\Software\Classes, and why. Windows registers each of
// its own packaged apps' file types, links and classes there as it
// provisions the app for a profile, at times of its own, under names of the
// app's, such as .all and windowsdefender on a clean runner, and trims its
// own caches there, such as Local Settings' ShellRefresh, which went during
// one dry run. So a key or value there that came, changed or went is passed
// over. Code Goblins registers nothing there but its notification
// registration and its click handlers, so everything in AppUserModelId and
// CLSID is compared. A key or value whose name, or what it held or holds,
// names Code Goblins, goblins, cfo.exe or goblins-window is named wherever it
// sits under Classes, even once it went.
func TestNoisePassesOverWindowsClassesButNeverOurs(t *testing.T) {
	cases := []struct {
		name string
		difference
		isNamed bool
	}{
		{"a packaged app's file type", difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\.all\OpenWithProgids`}, false},
		{"a packaged app's link", difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\windowsdefender`}, false},
		{"a packaged app's link's value", difference{Change: changeLeft, Kind: kindRegistryValue, Identity: `HKCU\Software\Classes\windowsdefender\URL Protocol`}, false},
		{"a value Windows changed", difference{Change: changeChanged, Kind: kindRegistryValue, Identity: `HKCU\Software\Classes\.txt\OpenWithProgids\AppXb5yxv86nkhp530y0y50yxe69c1qwad1x`, Before: "", After: "x"}, false},
		{"a key named for Code Goblins", difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\CodeGoblins.Brief`}, true},
		{"a link that runs the window", difference{Change: changeLeft, Kind: kindRegistryValue, Identity: `HKCU\Software\Classes\brief\shell\open\command\`, After: `"C:\Users\a\AppData\Local\CodeGoblins\bin\goblins-window.exe" "%1"`}, true},
		{"a link that runs cfo.exe", difference{Change: changeLeft, Kind: kindRegistryValue, Identity: `HKCU\Software\Classes\brief\shell\open\command\`, After: `C:\tools\cfo.exe open "%1"`}, true},
		{"a value changed to name the goblins", difference{Change: changeChanged, Kind: kindRegistryValue, Identity: `HKCU\Software\Classes\.txt\shell\open\command\`, Before: "notepad.exe %1", After: `goblins.exe %1`}, true},
		{"a class", difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\CLSID\{675d0139-5ced-40b2-8c52-7ff0dca6fc9d}`}, true},
		{"a class's value", difference{Change: changeLeft, Kind: kindRegistryValue, Identity: `HKCU\Software\Classes\CLSID\{675d0139-5ced-40b2-8c52-7ff0dca6fc9d}\LocalServer32\`, After: "x"}, true},
		{"a notification sender", difference{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\AppUserModelId\Some App`}, true},
		{"a cache value Windows trimmed", difference{Change: changeGone, Kind: kindRegistryValue, Identity: `HKCU\Software\Classes\Local Settings\Software\Microsoft\Windows\CurrentVersion\AppModel\ShellRefresh\2\`, Before: "1"}, false},
		{"a file type that went", difference{Change: changeGone, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\.txt`}, false},
		{"a value of ours that went", difference{Change: changeGone, Kind: kindRegistryValue, Identity: `HKCU\Software\Classes\Local Settings\Software\Microsoft\Windows\Shell\MuiCache\x`, Before: `C:\Users\a\AppData\Local\CodeGoblins\bin\goblins-window.exe`}, true},
		{"a key named for us that went", difference{Change: changeGone, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\CodeGoblins.Brief`}, true},
		{"a class that went", difference{Change: changeGone, Kind: kindRegistryKey, Identity: `HKCU\Software\Classes\CLSID\{675d0139-5ced-40b2-8c52-7ff0dca6fc9d}`}, true},
		{"a notification sender's value that went", difference{Change: changeGone, Kind: kindRegistryValue, Identity: `HKCU\Software\Classes\AppUserModelId\Some App\DisplayName`, Before: "Some App"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			before := snapshotOf(map[string]map[string]string{tc.Kind: {}})
			after := snapshotOf(map[string]map[string]string{tc.Kind: {}})
			switch tc.Change {
			case changeLeft:
				after = snapshotOf(map[string]map[string]string{tc.Kind: {tc.Identity: tc.After}})
			case changeChanged:
				before = snapshotOf(map[string]map[string]string{tc.Kind: {tc.Identity: tc.Before}})
				after = snapshotOf(map[string]map[string]string{tc.Kind: {tc.Identity: tc.After}})
			case changeGone:
				before = snapshotOf(map[string]map[string]string{tc.Kind: {tc.Identity: tc.Before}})
			}

			// Act
			remaining, _ := compare(before, after, nil, noise())

			// Assert
			if (len(remaining) == 1) != tc.isNamed {
				t.Errorf("%s: named %v, want named %v", tc.difference, remaining, tc.isNamed)
			}
		})
	}
}
