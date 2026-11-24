package reap

import "testing"

// TestClassifyProcessPopulations is the fix for four identical sweeps in
// ninety minutes, fourteen findings each, every one a false positive. The
// image name says nothing on this machine: claude.exe is the Overlord's
// desktop application a dozen times over and a reviewer of a round in
// progress, as often as it is a fleet harness. The parent process and the
// command line are what tell them apart, and only the last case here is
// something the sweep has any business reporting.
func TestClassifyProcessPopulations(t *testing.T) {
	const (
		herdrPID        = 100
		desktopPID      = 700
		codexDesktopPID = 750
		gatePID         = 800
	)
	const desktopExe = `"C:\Program Files\WindowsApps\Claude_2.2553.1.0_x64__pzs8sxrjxfjjc\app\claude.exe"`
	const codexDesktopExe = `"C:\Program Files\WindowsApps\OpenAI.Codex_26.924.2738.0_x64__2p2nqsd0c76g0\app\ChatGPT.exe"`
	// One machine, all four populations at once, because that is how they
	// actually appear and a fixture with one at a time would not prove the
	// classifier keeps them apart.
	processes := []Process{
		process(herdrPID, 1, "herdr.exe", "herdr server", fixtureStart),
		// The desktop application: one parent under sihost, then the Chromium
		// children it spawns, each carrying a --type= switch.
		process(desktopPID, 7996, "claude.exe", desktopExe+" ", fixtureStart),
		process(701, desktopPID, "claude.exe", desktopExe+" --type=renderer --user-data-dir=...", fixtureLater),
		process(702, desktopPID, "claude.exe", desktopExe+" --type=gpu-process --gpu-preferences=...", fixtureLater),
		process(703, desktopPID, "claude.exe", desktopExe+" --type=crashpad-handler --user-data-dir=...", fixtureLater),
		// The Codex desktop application: ChatGPT.exe from its package, its
		// Chromium children, and the codex.exe app server it runs from
		// outside the package.
		process(codexDesktopPID, 9376, "ChatGPT.exe", codexDesktopExe+" ", fixtureStart),
		process(751, codexDesktopPID, "ChatGPT.exe", codexDesktopExe+" --type=renderer --standard-schemes=app,codex-sandbox", fixtureLater),
		process(752, codexDesktopPID, "codex.exe", `C:\Users\op\AppData\Local\OpenAI\Codex\bin\faa963e871dd422c\codex.exe -c features.code_mode_host=true app-server --analytics-default-enabled`, fixtureLater),
		// A no-mistakes review round: the daemon and the reviewer it launched.
		process(gatePID, 1, "no-mistakes.exe", `no-mistakes.exe daemon run --root C:\Users\x\.no-mistakes`, fixtureStart),
		process(801, gatePID, "claude.exe", `claude --model opus --effort high -p --verbose --output-format stream-json --json-schema "{}"`, fixtureLater),
		// The real thing: a harness that descends from the Herdr server and
		// has no pane left.
		process(400, herdrPID, "powershell.exe", "powershell -NoExit", fixtureLater),
		process(31032, 400, "claude.exe", `claude --dangerously-skip-permissions --strict-mcp-config`, fixtureLatest),
	}
	findings := classOf(Classify(Inventory{FleetRootPIDs: []int{herdrPID}, Processes: processes}), OrphanProcess)

	reported := make(map[int]Finding, len(findings))
	for _, finding := range findings {
		reported[finding.PID] = finding
	}
	for _, unwanted := range []struct {
		pid        int
		population string
	}{
		{desktopPID, "the desktop application the Overlord is using"},
		{701, "a desktop application renderer child"},
		{702, "a desktop application gpu-process child"},
		{703, "a desktop application crashpad-handler child"},
		{752, "the Codex desktop application's app server"},
		{801, "a gate agent reviewing for a goblin that is working"},
	} {
		if finding, ok := reported[unwanted.pid]; ok {
			t.Errorf("pid %d (%s) was reported: %s", unwanted.pid, unwanted.population, finding.Line())
		}
	}
	orphan, ok := reported[31032]
	if !ok {
		t.Fatalf("the genuine orphan was not reported; findings: %+v", findings)
	}
	if want := killNeedsItsOwnPID + ". Name 31032 with --force to take responsibility for it"; orphan.Hold() != want {
		t.Errorf("hold = %q, want the genuine orphan held by nothing but its kill authorisation: %q", orphan.Hold(), want)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d orphan_process findings, want only the genuine orphan: %+v", len(findings), findings)
	}
}
