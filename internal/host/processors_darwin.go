package host

import "slices"

// terminalStart is the command spec's terminal starts and the mask of the
// processor threads it keeps to, 0, the threads the host may run on, since
// macOS keeps no process to processor threads. A terminal told to leave cores
// for apps starts through taskpolicy clamped to the utility class, Apple's
// class for long work the user is not waiting on, which taskpolicy sets before
// it runs the command in its own place, so the terminal's process is the
// command's and everything it starts keeps the clamp. The clamp gives it a
// lower scheduling priority than a default program's and keeps it off no
// core. On the macOS runner on 2026-11-27, a virtual machine with one kind of
// core, it made a cold build about 1.2 times as long (11.0 s against 9.2 s)
// and gave a foreground load beside it no measured gain, while background
// priority made the build 6.4 times as long (58.7 s) and throttles disk and
// network (#2535).
func terminalStart(spec Spec) ([]string, uintptr) {
	if !spec.ShouldLeaveCoresForApps {
		return spec.Args, 0
	}
	return slices.Concat([]string{"/usr/sbin/taskpolicy", "-c", "utility"}, spec.Args), 0
}
