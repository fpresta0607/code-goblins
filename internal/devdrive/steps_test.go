package devdrive

import (
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestAMachineThatCannotHaveOneGetsNoStep(t *testing.T) {
	for name, change := range map[string]func(*Machine){
		"Windows 10":    func(m *Machine) { m.Build = 19045 },
		"Defender off":  func(m *Machine) { m.Defender = false },
		"no room on C:": func(m *Machine) { m.SystemFree = 40 * gigabyte },
	} {
		m := ready()
		change(&m)
		if plan, ok := Next(m, home.Home{Root: `C:\home`}, home.DevDriveConfig{}, false, false); ok {
			t.Errorf("%s: plan = %+v, want none", name, plan)
		}
	}
}

// The scripts run as administrator from the Command Center, so what they
// may do is checked here: never a Defender setting or exclusion, never the
// switch that takes the antivirus filter off every Dev Drive.
func TestTheScriptsNeverTouchDefender(t *testing.T) {
	create := CreateScript(`C:\DevDrives\CodeGoblins.vhdx`, "D:", 200)
	for _, script := range []string{create, TrustScript("D:")} {
		for _, never := range []string{"MpPreference", "Exclusion", "disallowAv", "DisableRealtime"} {
			if strings.Contains(strings.ToLower(script), strings.ToLower(never)) {
				t.Errorf("a script names %q:\n%s", never, script)
			}
		}
	}
	for _, step := range []string{"create vdisk file=", "maximum=204800 type=expandable", "Mount-DiskImage", "Format-Volume -DevDrive", "Register-ScheduledTask -TaskName 'Code Goblins Dev Drive' -Trigger (New-ScheduledTaskTrigger -AtStartup)", "icacls $folder /inheritance:r", "fsutil devdrv query"} {
		if !strings.Contains(create, step) {
			t.Errorf("the create script lacks %q", step)
		}
	}
	// A path with a quote in it stays one PowerShell string.
	if script := CreateScript(`C:\Users\o'neil\CodeGoblins.vhdx`, "D:", 50); !strings.Contains(script, `$vhd = 'C:\Users\o''neil\CodeGoblins.vhdx'`) {
		t.Errorf("a quote in the path was not doubled:\n%s", script)
	}
}
