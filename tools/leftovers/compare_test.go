package main

import (
	"slices"
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
		{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\AppData\Local\Temp\jiti\extensions-goblins-native.291f97c5.mjs`},
		{Change: changeLeft, Kind: kindFile, Identity: `C:\Users\a\AppData\Local\goblins\conpty\OpenConsole.exe`},
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

// What the runner's Azure guest agent left in run 38523676119, its log
// collector running and the empty keys above a 32-bit list of installed apps
// in the profile, is passed over, and a planted Code Goblins leftover beside
// it, or under it, is still named.
func TestNoisePassesOverWhatTheRunnersGuestAgentLeavesButNotOurs(t *testing.T) {
	t.Setenv("SystemDrive", `C:`)
	t.Setenv("SystemRoot", `C:\Windows`)
	guestAgent := []difference{
		{Change: changeLeft, Kind: kindProcess, Identity: `C:\WindowsAzure\GuestAgent_2.7.41491.1243_2026-10-14_054847\CollectGuestLogs.exe`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Wow6432Node\Microsoft\Windows`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Wow6432Node\Microsoft\Windows\CurrentVersion`},
		{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Wow6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	}
	cases := []struct {
		name    string
		planted []difference
	}{
		{"nothing of ours", nil},
		{"our program under the guest agent's folder", []difference{{Change: changeLeft, Kind: kindProcess, Identity: `C:\WindowsAzure\GuestAgent_2.7.41491.1243_2026-10-14_054847\goblins.exe`}}},
		{"another program of the guest agent's", []difference{{Change: changeLeft, Kind: kindProcess, Identity: `C:\WindowsAzure\GuestAgent_2.7.41491.1243_2026-10-14_054847\Other.exe`}}},
		{"a log collector outside the guest agent's folder", []difference{{Change: changeLeft, Kind: kindProcess, Identity: `C:\Users\a\AppData\Local\CodeGoblins\bin\CollectGuestLogs.exe`}}},
		{"our entry in the 32-bit list", []difference{{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Wow6432Node\Microsoft\Windows\CurrentVersion\Uninstall\CodeGoblins`}}},
		{"any entry in the 32-bit list", []difference{{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Wow6432Node\Microsoft\Windows\CurrentVersion\Uninstall\{0123}`}}},
		{"a key beside the 32-bit list", []difference{{Change: changeLeft, Kind: kindRegistryKey, Identity: `HKCU\Software\Wow6432Node\Microsoft\Windows\CurrentVersion\Run`}}},
		{"an entry of the 32-bit list gone", []difference{{Change: changeGone, Kind: kindRegistryKey, Identity: `HKCU\Software\Wow6432Node\Microsoft\Windows\CurrentVersion\Uninstall\Old`}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			before, after := newSnapshot(time.Unix(0, 0)), newSnapshot(time.Unix(0, 0))
			for _, difference := range append(slices.Clone(guestAgent), tc.planted...) {
				if difference.Change == changeGone {
					before.add(difference.Kind, difference.Identity, "")
					continue
				}
				after.add(difference.Kind, difference.Identity, "")
			}

			// Act
			remaining, _ := compare(before, after, nil, noise())

			// Assert
			if len(remaining) != len(tc.planted) || len(tc.planted) == 1 && remaining[0].Identity != tc.planted[0].Identity {
				t.Errorf("named %v, want %v named", remaining, tc.planted)
			}
		})
	}
}

// Windows' own Microsoft account and Live sign-in write a credential for the
// profile by themselves, at a time of their own: in run 38978481733 four of
// five jobs' runners held WindowsLive:target=virtualapp/didlogical before the
// install began, and the fifth came to hold it during its job, which named it
// left by the install. Code Goblins writes only credentials named ceo:, so
// that one is passed over and ours, one beside it and one the uninstall took
// are still named.
func TestNoisePassesOverWindowsOwnLiveCredentialButNeverOurs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		difference difference
		isNamed    bool
	}{
		{"Windows' own Live credential, come during the run", difference{Change: changeLeft, Kind: kindCredential, Identity: `WindowsLive:target=virtualapp/didlogical`}, false},
		{"our credential", difference{Change: changeLeft, Kind: kindCredential, Identity: `LegacyGeneric:target=ceo:DEMO_TOKEN`}, true},
		{"a Live credential naming ceo", difference{Change: changeLeft, Kind: kindCredential, Identity: `WindowsLive:target=virtualapp/ceo`}, true},
		{"another program's credential", difference{Change: changeLeft, Kind: kindCredential, Identity: `LegacyGeneric:target=git:https://github.com`}, true},
		{"Windows' own Live credential, taken by the uninstall", difference{Change: changeGone, Kind: kindCredential, Identity: `WindowsLive:target=virtualapp/didlogical`}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			before, after := newSnapshot(time.Unix(0, 0)), newSnapshot(time.Unix(0, 0))
			if tc.difference.Change == changeGone {
				before.add(tc.difference.Kind, tc.difference.Identity, "")
			} else {
				after.add(tc.difference.Kind, tc.difference.Identity, "")
			}

			// Act
			remaining, _ := compare(before, after, nil, noise())

			// Assert
			if (len(remaining) == 1) != tc.isNamed {
				t.Errorf("named %v, want named %t", remaining, tc.isNamed)
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
// names Code Goblins, goblins, goblins.exe or goblins-window is named wherever
// it sits under Classes, even once it went.
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
		{"a link that runs goblins.exe", difference{Change: changeLeft, Kind: kindRegistryValue, Identity: `HKCU\Software\Classes\brief\shell\open\command\`, After: `C:\tools\goblins.exe open "%1"`}, true},
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
