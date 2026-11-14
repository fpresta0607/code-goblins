package devdrive

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// The scripts cannot be run here, since they make and attach disks, so
// Windows PowerShell 5.1, which runs them, parses each and must find nothing
// wrong.
func TestWindowsPowerShellParsesTheScripts(t *testing.T) {
	for name, script := range map[string]string{
		"create": CreateScript(`C:\DevDrives\CodeGoblins.vhdx`, "D:", 200),
		"attach": CreateScript(`C:\DevDrives\CodeGoblins.vhdx`, "E:", 0),
		"trust":  TrustScript("D:"),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name+".ps1")
			if err := os.WriteFile(path, append([]byte("\xef\xbb\xbf"), script...), 0o600); err != nil {
				t.Fatal(err)
			}
			check := "$errors = $null; [void][System.Management.Automation.Language.Parser]::ParseFile('" + path + "', [ref]$null, [ref]$errors); $errors | ForEach-Object { $_.Message }; exit $errors.Count"
			output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", check).CombinedOutput()
			if err != nil || strings.TrimSpace(string(output)) != "" {
				t.Errorf("Windows PowerShell finds the %s script wrong: %v\n%s\n%s", name, err, output, script)
			}
		})
	}
}

const lettersCD = 1<<2 | 1<<3

func TestNextStepsAreCreateThenMoveOnAMachineWithoutADevDrive(t *testing.T) {
	// Arrange
	m := ready()
	m.Letters = 1 << 2 // C: only
	h := home.Home{Root: `C:\Users\op\AppData\Local\CodeGoblins`}

	// Act
	plan, ok := Next(m, h, home.DevDriveConfig{}, false, false)

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
	if plan, _ := Next(m, h, home.DevDriveConfig{}, false, false); !strings.Contains(plan.Title, "Dev Drive E:") || !strings.Contains(plan.Script, "$letter = 'E'") {
		t.Errorf("with D: taken the plan is %q, want E:", plan.Title)
	}

	// Once the create step made D:, the move is next.
	m.Volumes = append(m.Volumes, Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: Trusted, Free: 199 * gigabyte})
	plan, ok = Next(m, h, home.DevDriveConfig{VHD: `C:\DevDrives\CodeGoblins.vhdx`}, false, false)
	if !ok || plan.Step != StepMove || plan.Admin || plan.Folder != `D:\CodeGoblins` || !strings.Contains(plan.Title, `the gate's home moves to D:\CodeGoblins\no-mistakes once no gate run is in flight`) {
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
		plan, ok := Next(m, h, home.DevDriveConfig{}, false, false)
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
	if plan, ok := Next(m, moved, config, false, false); ok {
		t.Errorf("a home on its trusted Dev Drive got %+v", plan)
	}
	m.Volumes = m.Volumes[:1]
	plan, ok := Next(m, moved, config, false, false)
	if !ok || plan.Step != StepAttach || !strings.Contains(plan.Title, "Attach the Code Goblins Dev Drive D: again") || !strings.Contains(plan.Script, `$vhd = 'C:\DevDrives\CodeGoblins.vhdx'`) {
		t.Errorf("plan = %+v, %v; want the attach step for D:", plan, ok)
	}
}

func TestAHomeOnItsDevDriveGetsTheGateStepWhileTheGateHomeIsNotThere(t *testing.T) {
	m := ready()
	m.Volumes = append(m.Volumes, Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: Trusted})
	moved := home.Home{Root: `C:\home`, DevDrive: `D:\CodeGoblins`}
	for _, test := range []struct {
		name               string
		shouldMoveGateHome bool
	}{
		{name: "gate home still to move", shouldMoveGateHome: true},
		{name: "gate home in place"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			plan, ok := Next(m, moved, home.DevDriveConfig{}, test.shouldMoveGateHome, false)

			// Assert
			if ok != test.shouldMoveGateHome {
				t.Fatalf("plan = %+v, %v; want a gate step %v", plan, ok, test.shouldMoveGateHome)
			}
			if test.shouldMoveGateHome && (plan.Step != StepGate || plan.Admin || plan.Folder != `D:\CodeGoblins` || plan.Title != "") {
				t.Errorf("plan = %+v; want the gate step into D:\\CodeGoblins", plan)
			}
		})
	}
}
