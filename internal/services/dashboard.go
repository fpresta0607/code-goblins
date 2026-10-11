package services

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// Desktop is the Windows desktop as an engine start sees it: the processes
// running, the arguments one was started with, and the windows one shows.
type Desktop struct {
	Processes    func() ([]proc.Entry, error)
	Arguments    func(pid int) ([]string, error)
	CloseWindows func(pid int) (int, error)
	// Every is how often a start looks for the dashboard window, and so the
	// most it shows before it is closed, and Grace how long after the start
	// returned it keeps looking.
	Every time.Duration
	Grace time.Duration
}

// WindowsDesktop is this machine's desktop.
var WindowsDesktop = Desktop{
	Processes:    proc.Processes,
	Arguments:    proc.Arguments,
	CloseWindows: proc.CloseWindows,
	Every:        50 * time.Millisecond,
	Grace:        30 * time.Second,
}

// dockerDesktopImage is the program Docker Desktop's launcher, its window
// process and that process's Electron helpers all run as.
const dockerDesktopImage = "Docker Desktop.exe"

// dashboardLook is what a look for the dashboard found: the start's window
// process, when it found one, and whether it closed the dashboard.
type dashboardLook struct {
	pid      int
	isClosed bool
	err      error
}

// closeDashboard closes the dashboard window Docker Desktop opens as an
// engine start that began at began starts it, and only that window.
//
// Docker Desktop 4.86 opens its dashboard on every start while its setting
// "Open the Docker Dashboard when Docker Desktop starts" is on, and it is on
// unless someone turned it off: docker desktop start runs Docker Desktop's
// launcher, which starts its backend, which starts the window process with
// --name=dashboard, which shows the dashboard once it has drawn it. Neither
// docker desktop start, the launcher nor the window process takes any other
// word for it. The setting is the Overlord's, and cfo leaves it alone. So this
// finds the window process the start started, by its program, its arguments
// and its start, and once a window of it shows asks that window to close,
// as its close button would, which leaves Docker Desktop in the tray and
// its engine running. With the setting off the window process starts with
// no --name=dashboard, and the look ends there.
func (d Desktop) closeDashboard(ctx context.Context, began time.Time) dashboardLook {
	look := time.NewTicker(d.Every)
	defer look.Stop()
	var found dashboardLook
	for {
		if found.pid == 0 {
			pid, isDashboard, err := d.windowProcess(began)
			if err != nil {
				return dashboardLook{err: err}
			}
			if pid != 0 && !isDashboard {
				return dashboardLook{}
			}
			found.pid = pid
		}
		if found.pid != 0 {
			asked, err := d.CloseWindows(found.pid)
			if err != nil {
				found.err = err
				return found
			}
			if asked > 0 {
				found.isClosed = true
				return found
			}
		}
		select {
		case <-ctx.Done():
			return found
		case <-look.C:
		}
	}
}

// windowProcess finds the window process Docker Desktop started since began,
// and whether it opens the dashboard: the one its backend started, which
// names why with --reason, unlike its launcher, and is no Electron helper,
// each of which names its --type.
func (d Desktop) windowProcess(began time.Time) (int, bool, error) {
	processes, err := d.Processes()
	if err != nil {
		return 0, false, err
	}
	for _, process := range processes {
		if !strings.EqualFold(process.ExeBase, dockerDesktopImage) || process.Start.Before(began) {
			continue
		}
		arguments, err := d.Arguments(process.PID)
		if err != nil {
			// A process that ended since the list cannot be read, and is no
			// window process to close.
			continue
		}
		isNamed := func(prefix string) bool {
			return slices.ContainsFunc(arguments, func(argument string) bool { return strings.HasPrefix(argument, prefix) })
		}
		if !isNamed("--reason=") || isNamed("--type=") {
			continue
		}
		return process.PID, slices.Contains(arguments, "--name=dashboard"), nil
	}
	return 0, false, nil
}
