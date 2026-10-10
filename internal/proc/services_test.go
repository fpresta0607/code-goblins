package proc

import (
	"testing"
	"time"
)

// A goblin may start Docker Desktop or the no-mistakes daemon for its work,
// but each serves the whole machine: on 2026-10-07 a pause stopped Docker
// Desktop.exe, com.docker.build.exe, wsl.exe and wslhost.exe as a goblin's
// own, and on 2026-10-01 one stopped the daemon every gate shared. Each is
// known by its program, and whatever runs under one is that service's.
func TestServicesOfNamesEachMachineServiceAndWhatRunsUnderIt(t *testing.T) {
	// Arrange
	at := time.Date(2026, 10, 7, 12, 45, 0, 0, time.UTC)
	later := func(seconds int) time.Time { return at.Add(time.Duration(seconds) * time.Second) }
	processes := []ServiceProcess{
		{PID: 10, ParentPID: 1, ExeBase: "pwsh.exe", Start: later(0)},
		{PID: 11, ParentPID: 10, ExeBase: "Docker Desktop.exe", Arguments: []string{`C:\Program Files\Docker\Docker\Docker Desktop.exe`}, Start: later(1)},
		{PID: 12, ParentPID: 11, ExeBase: "com.docker.backend.exe", Start: later(2)},
		{PID: 13, ParentPID: 12, ExeBase: "wsl.exe", Arguments: []string{"wsl.exe", "-d", "docker-desktop"}, Start: later(3)},
		{PID: 14, ParentPID: 13, ExeBase: "wslhost.exe", Start: later(4)},
		{PID: 15, ParentPID: 10, ExeBase: "com.docker.build.exe", Start: later(5)},
		{PID: 20, ParentPID: 10, ExeBase: "no-mistakes.exe", Arguments: []string{`C:\tools\no-mistakes.exe`, "daemon", "run", "--root", `C:\Users\o\.no-mistakes`}, Start: later(6)},
		{PID: 21, ParentPID: 20, ExeBase: "claude.exe", Start: later(7)},
		{PID: 22, ParentPID: 10, ExeBase: "no-mistakes.exe", Arguments: []string{`C:\tools\no-mistakes.exe`, "axi", "run", "--intent", "ship"}, Start: later(8)},
		{PID: 30, ParentPID: 10, ExeBase: "wsl.exe", Arguments: []string{"wsl.exe", "-e", "go", "test"}, Start: later(9)},
		{PID: 31, ParentPID: 10, ExeBase: "node.exe", Start: later(10)},
		// A process whose recorded parent ID now names a Docker process that
		// started after it: Windows reused the ended parent's ID.
		{PID: 40, ParentPID: 41, ExeBase: "node.exe", Start: later(11)},
		{PID: 41, ParentPID: 11, ExeBase: "com.docker.proxy.exe", Start: later(12)},
		// A loop in recorded parents ends the walk.
		{PID: 50, ParentPID: 51, ExeBase: "a.exe", Start: later(13)},
		{PID: 51, ParentPID: 50, ExeBase: "b.exe", Start: later(13)},
	}

	// Act
	services := ServicesOf(processes)

	// Assert
	want := map[int]Service{11: DockerDesktop, 12: DockerDesktop, 13: DockerDesktop, 14: DockerDesktop, 15: DockerDesktop, 41: DockerDesktop, 20: GateDaemon, 21: GateDaemon}
	for _, process := range processes {
		if got := services[process.PID]; got != want[process.PID] {
			t.Errorf("%s pid %d is service %d, want %d", process.ExeBase, process.PID, got, want[process.PID])
		}
	}
}

// Only the daemon's own command makes no-mistakes a machine service: the
// command a goblin runs to drive its gate is the goblin's.
func TestServiceOfKnowsTheGateDaemonByItsCommand(t *testing.T) {
	for _, tc := range []struct {
		exe       string
		arguments []string
		want      Service
	}{
		{"no-mistakes.exe", []string{"no-mistakes", "daemon", "run"}, GateDaemon},
		{"no-mistakes.exe", []string{"no-mistakes", "daemon", "log-sink", "--root", "x"}, GateDaemon},
		{"NO-MISTAKES.EXE", []string{"no-mistakes", "Daemon", "run"}, GateDaemon},
		{"no-mistakes.exe", []string{"no-mistakes", "axi", "run"}, NoService},
		{"no-mistakes.exe", []string{"no-mistakes"}, NoService},
		{"no-mistakes.exe", nil, NoService},
		{"Docker Desktop.exe", nil, DockerDesktop},
		{"com.docker.backend.exe", nil, DockerDesktop},
		{"docker.exe", []string{"docker", "compose", "up"}, NoService},
		{"wsl.exe", []string{"wsl.exe"}, NoService},
	} {
		if got := ServiceOf(tc.exe, tc.arguments); got != tc.want {
			t.Errorf("ServiceOf(%q, %q) = %d, want %d", tc.exe, tc.arguments, got, tc.want)
		}
	}
}

// The Scrawl server keeps every goblin's review page, and whichever goblin's
// command found none running started it, in that goblin's folder and with its
// environment. Only the server's own script makes node that service: the
// command a goblin runs to open a page is the goblin's, and so is any other
// program's server.
func TestServiceOfKnowsTheScrawlServerByItsScript(t *testing.T) {
	for _, tc := range []struct {
		exe       string
		arguments []string
		want      Service
	}{
		{"node.exe", []string{`C:\Program Files\nodejs\node.exe`, `C:\Users\o\AppData\Roaming\npm\node_modules\lavish-axi\dist\server.mjs`, "server", "--port", "4455"}, PageServer},
		{"node.exe", []string{"node", `C:\src\lavish-axi\bin\lavish-axi-server.js`, "server", "--port", "4455"}, PageServer},
		{"NODE.EXE", []string{"node", "C:/Users/o/AppData/Roaming/npm/node_modules/Lavish-Axi/dist/server.mjs", "server"}, PageServer},
		{"node.exe", []string{"node", `C:\Users\o\AppData\Roaming\npm\node_modules\lavish-axi\dist\cli.mjs`, "open", "page.html"}, NoService},
		{"node.exe", []string{"node", `C:\work\app\dist\server.mjs`, "server"}, NoService},
		{"node.exe", []string{"node", `C:\Users\o\AppData\Roaming\npm\node_modules\lavish-axi\dist\server.mjs`}, NoService},
		{"bash.exe", []string{"bash", `C:\src\lavish-axi\bin\lavish-axi-server.js`, "server"}, NoService},
		{"node.exe", nil, NoService},
	} {
		if got := ServiceOf(tc.exe, tc.arguments); got != tc.want {
			t.Errorf("ServiceOf(%q, %q) = %d, want %d", tc.exe, tc.arguments, got, tc.want)
		}
	}
}
