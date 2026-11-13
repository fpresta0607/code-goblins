package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

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
			after:  map[string]map[string]string{kindProcess: {`C:\x\goblins.exe`: ""}},
			rules:  []rule{{kind: kindProcess, change: changeGone, pattern: regexp.MustCompile(`.`), why: "ended"}},
			want:   []string{`left process C:\x\goblins.exe`},
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
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Microsoft\Windows\CurrentVersion\PushNotifications\Backup\ceo`},
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

// Codex keeps a temporary folder of its own, .tmp in its home, where it
// clones its plugin marketplace into a folder named plugins-clone and a
// random word and removes the clone once it holds the plugins, at times of
// its own. In run 41515917655 the goblin's Codex removed plugins-clone-RpPvcL
// between the two snapshots around the uninstall's dry run, which named every
// file of it taken by the dry run. Code Goblins writes nothing there, so what
// comes, changes or goes there is passed over, while what the install leaves
// in the Codex home beside it, a thing of ours inside it and a file of the
// person's taken from the home are still named.
func TestNoisePassesOverCodexOwnTemporaryFolderButNotWhatTheInstallLeaves(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\a`)
	cases := []struct {
		name string
		difference
		isNamed bool
	}{
		{"a plugin clone Codex removed", difference{Change: changeGone, Kind: kindFile, Identity: `C:\Users\a\.codex\.tmp\plugins-clone-RpPvcL`, Before: "folder"}, false},
		{"a file of a plugin clone Codex removed", difference{Change: changeGone, Kind: kindFile, Identity: `C:\Users\a\.codex\.tmp\plugins-clone-RpPvcL\.agents\plugins\marketplace.json`, Before: "sha256 1"}, false},
		{"git's lock in a plugin clone Codex removed", difference{Change: changeGone, Kind: kindFile, Identity: `C:\Users\a\.codex\.tmp\plugins-clone-RpPvcL\.git\index.lock`}, false},
		{"a plugin clone Codex made", difference{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\.codex\.tmp\plugins-clone-Qw3rTy`, After: "folder"}, false},
		{"a file of a plugin clone Codex made", difference{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\.codex\.tmp\plugins-clone-Qw3rTy\.git\HEAD`, After: "sha256 1"}, false},
		{"the temporary folder itself, come during the run", difference{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\.codex\.tmp`, After: "folder"}, false},
		{"the temporary folder under another case", difference{Change: changeGone, Kind: kindFile, Identity: `c:\users\a\.codex\.tmp\plugins-clone-RpPvcL`, Before: "folder"}, false},
		{"Codex's record of the plugins it holds, changed", difference{Change: changeChanged, Kind: kindFile, Identity: `C:\Users\a\.codex\.tmp\plugins.sha`, Before: "sha256 1", After: "sha256 2"}, false},
		{"a skill the install left in the Codex home", difference{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\.codex\skills\gh-axi\SKILL.md`, After: "sha256 1"}, true},
		{"the Codex hooks the install changed", difference{Change: changeChanged, Kind: kindFile, Identity: `C:\Users\a\.codex\hooks.json`, Before: "sha256 1", After: "sha256 2"}, true},
		{"the person's Codex settings taken", difference{Change: changeGone, Kind: kindFile, Identity: `C:\Users\a\.codex\config.toml`, Before: "sha256 1"}, true},
		{"a folder beside the temporary folder", difference{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\.codex\.tmp-kept\note.txt`}, true},
		{"a file of ours in the temporary folder", difference{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\.codex\.tmp\goblins-native-hook.ps1`}, true},
		{"a temporary folder in another tool's home", difference{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\.pi\.tmp\plugins-clone-Qw3rTy`, After: "folder"}, true},
		{"a temporary folder of the same name outside the Codex home", difference{Change: changeGone, Kind: kindFile, Identity: `C:\Users\a\source\demo\.codex\.tmp\notes.txt`}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			before, after := newSnapshot(time.Unix(0, 0)), newSnapshot(time.Unix(0, 0))
			if tc.Change != changeLeft {
				before.add(tc.Kind, tc.Identity, tc.Before)
			}
			if tc.Change != changeGone {
				after.add(tc.Kind, tc.Identity, tc.After)
			}

			// Act
			remaining, _ := compare(before, after, nil, noise())

			// Assert
			if (len(remaining) == 1) != tc.isNamed {
				t.Errorf("%s: named %v, want named %t", tc.difference, remaining, tc.isNamed)
			}
		})
	}
}
