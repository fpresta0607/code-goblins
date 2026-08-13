package digest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// newDigestHome builds a bare home.Home over a fresh temp dir with empty
// state/ and data/ subdirectories: enough for Compose, which never requires
// AGENTS.md or a git checkout (that gate is the hook dispatcher's job, not
// Compose's).
func newDigestHome(t *testing.T) home.Home {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	return home.Home{Root: root, State: state, Data: data}
}

// startLiveForeignProcess spawns a throwaway child that stays alive for the
// test's duration, for fixtures that need a live foreign PID to pre-hold the
// session lock. Mirrors the ping-child pattern cmd/cfo's own hook tests use.
func startLiveForeignProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

// assertHeaderOrder asserts every header in order appears in output, each
// strictly after the previous one, via successive strings.Index comparisons.
func assertHeaderOrder(t *testing.T, output string, headers []string) {
	t.Helper()
	prev := -1
	for _, h := range headers {
		idx := strings.Index(output, h)
		if idx == -1 {
			t.Fatalf("missing header %q in output:\n%s", h, output)
		}
		if idx <= prev {
			t.Fatalf("header %q at index %d, want it after index %d\noutput:\n%s", h, idx, prev, output)
		}
		prev = idx
	}
}

var sectionHeaders = []string{
	"== SESSION LOCK ==",
	"== WAKE QUEUE ==",
	"== SUPERVISION OPERATING INSTRUCTIONS ==",
	"== READ-ONCE CONTRACT ==",
	"== FLEET STATE ==",
	"== CONTEXT ==",
	"== NEXT STEP ==",
}

func TestComposeSectionOrder(t *testing.T) {
	h := newDigestHome(t)

	backlog := "- [ ] queued task one\n- [ ] queued task two\n- [ ] queued task three\n- [x] done task one\n"
	if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(backlog), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "g1.meta"), []byte("goblin_id=g1\nstatus=running\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "g2.meta"), []byte("goblin_id=g2\nstatus=running\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "orphan.status"), []byte("orphan line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"projects.md", "overlord.md", "learnings.md"} {
		if err := os.WriteFile(filepath.Join(h.Data, name), []byte("content of "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := wake.Append(h.State, "signal", "g1.status", "goblin g1 finished"); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Append(h.State, "heartbeat", "heartbeat", "heartbeat"); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.PublishEpisode(h.State); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Compose(h, os.Getpid(), "s1", &buf); err != nil {
		t.Fatalf("Compose: %v", err)
	}

	out := buf.String()
	assertHeaderOrder(t, out, sectionHeaders)

	if !strings.Contains(out, "WAKE_ACK_REQUIRED: cfo drain --ack-through 2 --recovery-generation 1") {
		t.Errorf("output missing the expected WAKE_ACK_REQUIRED line (seq 2, generation 1):\n%s", out)
	}
}

func TestComposeBacklogCompact(t *testing.T) {
	h := newDigestHome(t)

	var b strings.Builder
	for i := 1; i <= 25; i++ {
		fmt.Fprintf(&b, "- [ ] queued row %d\n", i)
	}
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&b, "- [x] done row %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Compose(h, os.Getpid(), "s1", &buf); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	out := buf.String()

	for i := 1; i <= 20; i++ {
		want := fmt.Sprintf("queued row %d", i)
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	for i := 21; i <= 25; i++ {
		unwanted := fmt.Sprintf("queued row %d", i)
		if strings.Contains(out, unwanted) {
			t.Errorf("output unexpectedly contains %q (past QueuedLimit)", unwanted)
		}
	}
	if !strings.Contains(out, "(+5 more queued)") {
		t.Errorf("output missing the exact overflow line \"(+5 more queued)\":\n%s", out)
	}
	for i := 1; i <= 3; i++ {
		unwanted := fmt.Sprintf("done row %d", i)
		if strings.Contains(out, unwanted) {
			t.Errorf("output unexpectedly contains done row text %q", unwanted)
		}
	}
}

func TestComposeAbsentContext(t *testing.T) {
	h := newDigestHome(t)

	var buf bytes.Buffer
	if err := Compose(h, os.Getpid(), "s1", &buf); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if !strings.Contains(buf.String(), "overlord.md: ABSENT") {
		t.Errorf("output missing %q:\n%s", "overlord.md: ABSENT", buf.String())
	}
}

func TestComposeReadOnlyOnHeldLock(t *testing.T) {
	h := newDigestHome(t)

	foreign := startLiveForeignProcess(t)
	foreignPID := foreign.Process.Pid
	if _, err := lock.AcquireOwner(h.State, foreignPID, "other"); err != nil {
		t.Fatalf("pre-acquiring the foreign lock: %v", err)
	}

	var buf bytes.Buffer
	if err := Compose(h, os.Getpid(), "s1", &buf); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "●  READ-ONLY DIGEST - THIS SESSION DOES NOT HOLD THE HOME") {
		t.Errorf("output missing the read-only banner's second line:\n%s", out)
	}
	if !strings.Contains(out, strconv.Itoa(foreignPID)) {
		t.Errorf("output missing the foreign holder's pid %d:\n%s", foreignPID, out)
	}
	if _, err := os.Stat(filepath.Join(h.State, CompleteMarkerFile)); !os.IsNotExist(err) {
		t.Errorf("completion marker written despite a foreign-held lock: stat err = %v", err)
	}
}

func TestComposeWritesCompleteMarker(t *testing.T) {
	h := newDigestHome(t)
	pid := os.Getpid()

	var buf bytes.Buffer
	if err := Compose(h, pid, "s1", &buf); err != nil {
		t.Fatalf("Compose: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(h.State, CompleteMarkerFile))
	if err != nil {
		t.Fatalf("reading completion marker: %v", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("marker content %q does not parse as a pid: %v", string(data), err)
	}
	if got != pid {
		t.Errorf("marker pid = %d, want %d", got, pid)
	}
}

func TestComposeStatusTailCap(t *testing.T) {
	h := newDigestHome(t)

	if err := os.WriteFile(filepath.Join(h.State, "g1.meta"), []byte("goblin_id=g1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var statusLines strings.Builder
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&statusLines, "status line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(h.State, "g1.status"), []byte(statusLines.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Compose(h, os.Getpid(), "s1", &buf); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	out := buf.String()

	for i := 8; i <= 12; i++ {
		want := fmt.Sprintf("status line %d", i)
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q (should be in the last 5 lines):\n%s", want, out)
		}
	}
	if strings.Contains(out, "status line 6") {
		t.Errorf("output unexpectedly contains \"status line 6\" (the 7th-from-last line, should be capped away):\n%s", out)
	}

	// Second fixture: an orphan status file with no matching meta.
	h2 := newDigestHome(t)
	var orphanLines strings.Builder
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&orphanLines, "orphan content %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(h2.State, "orphan.status"), []byte(orphanLines.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf2 bytes.Buffer
	if err := Compose(h2, os.Getpid(), "s1", &buf2); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	out2 := buf2.String()
	if !strings.Contains(out2, "orphan.status") {
		t.Errorf("output missing the orphan status file's name:\n%s", out2)
	}
	for i := 1; i <= 12; i++ {
		unwanted := fmt.Sprintf("orphan content %d", i)
		if strings.Contains(out2, unwanted) {
			t.Errorf("output unexpectedly contains orphan status content %q (orphans get no tail):\n%s", unwanted, out2)
		}
	}
}

func TestComposeRendersUnreadableFilesInline(t *testing.T) {
	h := newDigestHome(t)

	if err := os.MkdirAll(filepath.Join(h.Data, "projects.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := Compose(h, os.Getpid(), "s1", &buf)
	if err != nil {
		t.Fatalf("Compose: %v, want nil (an unreadable file degrades one line, not the whole digest)", err)
	}
	out := buf.String()

	assertHeaderOrder(t, out, sectionHeaders)
	if !strings.Contains(out, "projects.md: UNREADABLE") {
		t.Errorf("output missing %q:\n%s", "projects.md: UNREADABLE", out)
	}
	if strings.Contains(out, "SESSION START DEGRADED") {
		t.Errorf("output unexpectedly contains SESSION START DEGRADED for a single bad file:\n%s", out)
	}
}
