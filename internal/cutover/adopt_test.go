package cutover

import (
	"os"
	"strings"
	"testing"
)

func TestAdoptOldSettingsReadsEachOldSettingByTheNewNameAndDropsTheOld(t *testing.T) {
	// Arrange
	old := strings.ToUpper(oldName) + "_"
	t.Setenv(old+"HOME", `D:\elsewhere`)
	t.Setenv(old+"STATE_OVERRIDE", `D:\elsewhere\state`)
	t.Setenv(setting+"STATE_OVERRIDE", `D:\set\state`)
	t.Setenv(setting+"HOME", "")
	os.Unsetenv(setting + "HOME")

	// Act
	AdoptOldSettings()

	// Assert
	for name, want := range map[string]string{setting + "HOME": `D:\elsewhere`, setting + "STATE_OVERRIDE": `D:\set\state`} {
		if got := os.Getenv(name); got != want {
			t.Errorf("%s is %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{old + "HOME", old + "STATE_OVERRIDE"} {
		if value, isSet := os.LookupEnv(name); isSet {
			t.Errorf("%s is still set, to %q", name, value)
		}
	}
}

func TestSettingInReadsAnotherProcesssSettingByItsOldNameOverTheNew(t *testing.T) {
	old := strings.ToUpper(oldName) + "_"
	cases := []struct {
		name        string
		environment []string
		setting     string
		want        string
	}{
		{"the old name only", []string{old + "HOME=" + `D:\elsewhere`}, setting + "HOME", `D:\elsewhere`},
		{"the old name in another case", []string{strings.ToLower(old) + "state_override=" + `D:\state`}, setting + "STATE_OVERRIDE", `D:\state`},
		{"the new name only", []string{setting + "HOME=" + `D:\new`}, setting + "HOME", `D:\new`},
		{"the old name over the new, the old first", []string{old + "HOME=" + `D:\old`, setting + "HOME=" + `D:\new`}, setting + "HOME", `D:\old`},
		{"the old name over the new, the new first", []string{setting + "HOME=" + `D:\new`, old + "HOME=" + `D:\old`}, setting + "HOME", `D:\old`},
		{"another setting's old name", []string{old + "HOMELY=" + `D:\old`}, setting + "HOME", ""},
		{"neither", []string{`LOCALAPPDATA=C:\local`}, setting + "HOME", ""},
		{"the CEO's terminal by its old name", []string{old + "HOST_ID=" + oldName}, hostIDSetting, newName},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := SettingIn(test.environment, test.setting)

			// Assert
			if got != test.want {
				t.Errorf("SettingIn(%q, %s) = %q, want %q", test.environment, test.setting, got, test.want)
			}
		})
	}
}

func TestHandBackSettingsGivesABuildFromBeforeTheRenameTheSettingsItWasHanded(t *testing.T) {
	// Arrange
	old := strings.ToUpper(oldName) + "_"
	t.Setenv(old+"HOME", `D:\elsewhere`)
	t.Setenv(old+"STATE_OVERRIDE", `D:\elsewhere\state`)
	t.Setenv(setting+"HOME", "")
	os.Unsetenv(setting + "HOME")
	t.Setenv(setting+"STATE_OVERRIDE", "")
	os.Unsetenv(setting + "STATE_OVERRIDE")
	AdoptOldSettings()

	// Act
	HandBackSettings()

	// Assert
	for name, want := range map[string]string{old + "HOME": `D:\elsewhere`, old + "STATE_OVERRIDE": `D:\elsewhere\state`, setting + "HOME": `D:\elsewhere`} {
		if got := os.Getenv(name); got != want {
			t.Errorf("%s is %q, want %q", name, got, want)
		}
	}
}

func TestAdoptOldSettingsNamesTheCEOsTerminalFromBeforeTheMoveByItsNewName(t *testing.T) {
	cases := []struct{ name, terminal, want string }{
		{name: "the CEO's", terminal: oldName, want: newName},
		{name: "a goblin's", terminal: "demo-goblin", want: "demo-goblin"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			old := strings.ToUpper(oldName) + "_"
			t.Setenv(old+"HOST_ID", test.terminal)
			t.Setenv(hostIDSetting, "")
			os.Unsetenv(hostIDSetting)

			// Act
			AdoptOldSettings()
			adopted := os.Getenv(hostIDSetting)
			HandBackSettings()

			// Assert
			if adopted != test.want {
				t.Errorf("%s is %q, want %q", hostIDSetting, adopted, test.want)
			}
			if handed := os.Getenv(old + "HOST_ID"); handed != test.terminal {
				t.Errorf("handed back %q, want %q", handed, test.terminal)
			}
		})
	}
}

func TestIsOldWakeLineKnowsOnlyAWakeLineABoardFromBeforeTheRenameTyped(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{name: "one wake", line: oldName + " watcher wake: 1 queued wake (check supervisor); run " + oldName + " drain, handle what it shows and ack it with the WAKE_ACK_REQUIRED command it prints", want: true},
		{name: "several wakes", line: oldName + " watcher wake: 5 queued wakes (notify a, idle b, error c, 2 more); run " + oldName + " drain, handle what it shows and ack it with the WAKE_ACK_REQUIRED command it prints", want: true},
		{name: "a line cut short", line: oldName + " watcher wake: 9 queued wakes (notify " + strings.Repeat("x", 280) + "...", want: true},
		{name: "his words after it", line: oldName + " watcher wake: 1 queued wake (check supervisor); run " + oldName + " drain, handle what it shows and ack it with the WAKE_ACK_REQUIRED command it prints and then merge", want: false},
		{name: "his words before it", line: "look at " + oldName + " watcher wake: 1 queued wake (check supervisor); run " + oldName + " drain, handle what it shows and ack it with the WAKE_ACK_REQUIRED command it prints", want: false},
		{name: "this build's wake line", line: newName + " watcher wake: 1 queued wake (check supervisor); run " + newCommand + " drain, handle what it shows and ack it with the WAKE_ACK_REQUIRED command it prints", want: false},
		{name: "an empty prompt line", line: "", want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			draft := strings.Join(strings.Fields(test.line), "")

			// Act
			got := IsOldWakeLine(draft)

			// Assert
			if got != test.want {
				t.Errorf("IsOldWakeLine(%q) = %v, want %v", draft, got, test.want)
			}
		})
	}
}
