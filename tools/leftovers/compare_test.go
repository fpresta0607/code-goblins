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
			name:       "a PATH entry naming a kept tool's folder",
			before:     map[string]map[string]string{kindPathEntry: {}},
			after:      map[string]map[string]string{kindPathEntry: {`user C:\Users\a\.local\bin`: ""}},
			kept:       []string{`C:\Users\a\.local\bin`},
			wantPassed: 1,
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
