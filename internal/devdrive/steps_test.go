package devdrive

import (
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

const lettersCD = 1<<2 | 1<<3

func TestNextStepsAreCreateThenMoveOnAMachineWithoutADevDrive(t *testing.T) {
	// Arrange
	m := ready()
	m.Letters = 1 << 2 // C: only
	h := home.Home{Root: `C:\Users\op\AppData\Local\CodeGoblins`}

	// Act
	plan, ok := Next(m, h, home.DevDriveConfig{})

	// Assert
	if !ok || plan.Step != StepCreate || !plan.Admin || plan.VHD != `C:\DevDrives\CodeGoblins.vhdx` {
		t.Fatalf("plan = %+v, %v; want the create step, as administrator, in C:\\DevDrives", plan, ok)
	}
	for _, says := range []string{"Create the Code Goblins Dev Drive D:", "200 GB", "startup task", "performance mode", "adds no exclusion"} {
		if !strings.Contains(plan.Title, says) {
			t.Errorf("title %q lacks %q", plan.Title, says)
		}
	}
	m.Letters = lettersCD
	if plan, _ := Next(m, h, home.DevDriveConfig{}); !strings.Contains(plan.Title, "Dev Drive E:") || !strings.Contains(plan.Script, "$letter = 'E'") {
		t.Errorf("with D: taken the plan is %q, want E:", plan.Title)
	}

	// Once the create step made D:, the move is next.
	m.Volumes = append(m.Volumes, Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: Trusted, Free: 199 * gigabyte})
	plan, ok = Next(m, h, home.DevDriveConfig{VHD: `C:\DevDrives\CodeGoblins.vhdx`})
	if !ok || plan.Step != StepMove || plan.Admin || plan.Folder != `D:\CodeGoblins` || !strings.Contains(plan.Title, "nothing is copied or deleted now") {
		t.Fatalf("plan = %+v, %v; want the move to D:\\CodeGoblins, without administrator", plan, ok)
	}
}

func TestAnUntrustedDevDriveIsTrustedFirst(t *testing.T) {
	m := ready()
	m.Volumes = append(m.Volumes, Volume{Root: `E:\`, FileSystem: "ReFS", Dev: true, Trust: Untrusted})
	for name, h := range map[string]home.Home{
		"before the move": {Root: `C:\home`},
		"after the move":  {Root: `C:\home`, DevDrive: `E:\CodeGoblins`},
	} {
		plan, ok := Next(m, h, home.DevDriveConfig{})
		if !ok || plan.Step != StepTrust || !plan.Admin || !strings.Contains(plan.Script, "fsutil devdrv trust E:") || !strings.Contains(plan.Title, "Trust the Dev Drive E:") {
			t.Errorf("%s: plan = %+v, %v; want the trust step for E:", name, plan, ok)
		}
	}
}

func TestAHomeOnItsDevDriveNeedsNothingUntilTheDriveGoesMissing(t *testing.T) {
	m := ready()
	m.Volumes = append(m.Volumes, Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: Trusted})
	moved := home.Home{Root: `C:\home`, DevDrive: `D:\CodeGoblins`}
	config := home.DevDriveConfig{VHD: `C:\DevDrives\CodeGoblins.vhdx`}
	if plan, ok := Next(m, moved, config); ok {
		t.Errorf("a home on its trusted Dev Drive got %+v", plan)
	}
	m.Volumes = m.Volumes[:1]
	plan, ok := Next(m, moved, config)
	if !ok || plan.Step != StepAttach || !strings.Contains(plan.Title, "Attach the Code Goblins Dev Drive D: again") || !strings.Contains(plan.Script, `$vhd = 'C:\DevDrives\CodeGoblins.vhdx'`) {
		t.Errorf("plan = %+v, %v; want the attach step for D:", plan, ok)
	}
}

func TestAMachineThatCannotHaveOneGetsNoStep(t *testing.T) {
	for name, change := range map[string]func(*Machine){
		"Windows 10":    func(m *Machine) { m.Build = 19045 },
		"Defender off":  func(m *Machine) { m.Defender = false },
		"no room on C:": func(m *Machine) { m.SystemFree = 40 * gigabyte },
	} {
		m := ready()
		change(&m)
		if plan, ok := Next(m, home.Home{Root: `C:\home`}, home.DevDriveConfig{}); ok {
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
