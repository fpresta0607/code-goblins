package proc

import (
	"os"
	"os/exec"
	"testing"
)

func TestAncestryIncludesSelfAndParent(t *testing.T) {
	entries, err := Ancestry(os.Getpid(), 16)
	if err != nil {
		t.Fatalf("Ancestry: %v", err)
	}
	if len(entries) < 1 || entries[0].PID != os.Getpid() {
		t.Fatalf("first entry must be self, got %+v", entries)
	}
	if entries[0].ExeBase == "" || entries[0].Start.IsZero() {
		t.Errorf("self entry incomplete: %+v", entries[0])
	}
	if len(entries) >= 2 && entries[1].PID != entries[0].ParentPID {
		t.Errorf("chain broken: %+v", entries[:2])
	}
}

func TestFindAncestorFindsSelfByName(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := baseNoExe(self)
	e, ok := FindAncestor(os.Getpid(), 16, base)
	if !ok || e.PID != os.Getpid() {
		t.Errorf("FindAncestor(%q) = %+v %v, want self", base, e, ok)
	}
}

func TestFindAncestorMissReturnsFalse(t *testing.T) {
	if _, ok := FindAncestor(os.Getpid(), 16, "no-such-process-name-xyz"); ok {
		t.Error("found an ancestor that cannot exist")
	}
}

func TestAncestryOfChildProcessSeesUs(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ping -n 3 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	entries, err := Ancestry(cmd.Process.Pid, 16)
	if err != nil {
		t.Fatalf("Ancestry(child): %v", err)
	}
	found := false
	for _, e := range entries {
		if e.PID == os.Getpid() {
			found = true
		}
	}
	if !found {
		t.Errorf("test process missing from child ancestry: %+v", entries)
	}
}
