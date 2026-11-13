package supervisor

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// warmStandInGit starts the stand-in git at path once and waits for it to
// answer. Windows scans a program the first time it starts, and for a copy of
// this test binary a loaded runner took over eight seconds to: on 2026-10-09
// the custody check's own deadline passed twice before the stand-in it had
// started could record that it ran (runs 37969744895 and 38012012787, each on
// a change that did not touch it). A stand-in that must answer inside a
// deadline the code under test sets is started here first, so the scan is no
// part of what that deadline measures.
func warmStandInGit(t *testing.T, path string) {
	t.Helper()
	begun := time.Now()
	defer func() { t.Logf("the stand-in git's first start took %s", time.Since(begun).Round(time.Millisecond)) }()
	if output, err := exec.Command(path, "--version").CombinedOutput(); err != nil || !strings.Contains(string(output), "git version stand-in") {
		t.Fatalf("the stand-in git did not start: %v\n%s", err, output)
	}
}
