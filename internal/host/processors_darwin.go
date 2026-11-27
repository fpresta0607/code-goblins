package host

import "slices"

// terminalStart is the command spec's terminal starts and the mask of the
// processor threads it keeps to, 0, the threads the host may run on, since
// macOS keeps no process to processor threads. A terminal told to leave cores
// for apps starts through taskpolicy clamped to the utility class, which
// taskpolicy sets before it runs the command in its own place, so the
// terminal's process is the command's and everything it starts keeps the
// clamp. Apple silicon runs that class on its efficiency cores when it can.
// Background priority keeps work off the performance cores as well, but it
// also throttles disk and network: on the macOS runner on 2026-11-27 it made
// a cold build 6.4 times as long (58.7 s against 9.2 s), where the utility
// class made it 1.2 times as long (11.0 s) (#2535).
func terminalStart(spec Spec) ([]string, uintptr) {
	if !spec.ShouldLeaveCoresForApps {
		return spec.Args, 0
	}
	return slices.Concat([]string{"/usr/sbin/taskpolicy", "-c", "utility"}, spec.Args), 0
}
