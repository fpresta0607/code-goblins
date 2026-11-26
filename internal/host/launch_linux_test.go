package host

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// cgroupOf is the cgroup process pid runs in.
func cgroupOf(t *testing.T, pid int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cgroup"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestALinuxHostRunsInAScopeOfItsOwn(t *testing.T) {
	// Arrange
	if err := scopeAnswers(withRuntimeFolder(os.Environ())); err != nil {
		t.Skipf("no systemd user manager makes scopes on this machine: %v", err)
	}

	// Act
	_, record := launchHost(t, hostEnvironment(), `sleep 600`)

	// Assert
	if record.Contained {
		t.Fatal("Launch reported the host contained where a scope was made")
	}
	if cgroup := cgroupOf(t, record.HostPID); !strings.HasSuffix(cgroup, ".scope") || cgroup == cgroupOf(t, os.Getpid()) {
		t.Fatalf("the host runs in cgroup %q, not a scope of its own", cgroup)
	}
}

func TestALinuxHostThatNoUserManagerScopesRunsContained(t *testing.T) {
	// Arrange
	previous := systemdRun
	systemdRun = "goblins-test-no-such-systemd-run"
	t.Cleanup(func() { systemdRun = previous })

	// Act
	stateDir, record := launchHost(t, hostEnvironment(), `echo serving; sleep 600`)

	// Assert
	if !record.Contained {
		t.Fatal("Launch did not report the host contained, though no scope could be made")
	}
	log, err := os.ReadFile(filepath.Join(stateDir, "hosts", hostID+".log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "no systemd user manager gives this host a scope of its own") {
		t.Fatalf("the host's log does not say it runs contained:\n%s", log)
	}
	if cgroup := cgroupOf(t, record.HostPID); cgroup != cgroupOf(t, os.Getpid()) {
		t.Fatalf("the contained host runs in cgroup %q, not its launcher's", cgroup)
	}
	watch(t, record).await(t, `serving`)
}
