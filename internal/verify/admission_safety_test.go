package verify

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

func TestAdmissionRejectsAnAlreadyCancelledWait(t *testing.T) {
	// Arrange
	a := admission(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	turn, err := a.Wait(ctx)
	defer turn.Release()

	// Assert
	if !errors.Is(err, context.Canceled) || turn.release != nil {
		t.Fatalf("a cancelled waiter acquired a turn: %+v, %v", turn, err)
	}
}

func TestAdmissionKeepsALiveOverdueProcessInCustody(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := exec.Command(os.Args[0])
	holder.Env = append(os.Environ(), "VERIFY_TEST_HOLD_DIR="+a.Dir)
	stdin, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		if err := holder.Wait(); err != nil {
			t.Error(err)
		}
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "holding" {
		t.Fatalf("holder: %q, %v", line, err)
	}
	before, err := lock.ReadNamed(a.Dir, "slot-1")
	if err != nil || !before.VerifiedAlive() {
		t.Fatalf("holder custody was not proved: %+v, %v", before, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 450*time.Millisecond)
	defer cancel()

	// Act
	turn, err := a.Wait(ctx)
	defer turn.Release()

	// Assert
	after, readErr := lock.ReadNamed(a.Dir, "slot-1")
	if !errors.Is(err, context.DeadlineExceeded) || turn.release != nil || readErr != nil || *after != *before || !after.VerifiedAlive() {
		t.Fatalf("a live overdue holder lost custody: turn=%+v err=%v holder=%+v read=%v", turn, err, after, readErr)
	}
}

func TestAdmissionProductionStoreCannotBeRedirected(t *testing.T) {
	// Arrange
	a := admission(t)
	previous := os.Args[0]
	os.Args[0] = "cfo.exe"
	t.Cleanup(func() { os.Args[0] = previous })

	// Act
	turn, err := a.Wait(context.Background())
	defer turn.Release()

	// Assert
	if err == nil || turn.release != nil {
		t.Fatalf("a production process created a private admission pool: %+v, %v", turn, err)
	}
	entries, readErr := os.ReadDir(a.Dir)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("rejected production store was mutated: %+v, %v", entries, readErr)
	}
}

func TestAdmissionRejectsPerProcessSlotSettings(t *testing.T) {
	// Arrange
	a := admission(t)
	t.Setenv("CFO_VERIFY_SLOTS", "2")

	// Act
	turn, err := a.Wait(context.Background())
	defer turn.Release()

	// Assert
	if err == nil || turn.release != nil {
		t.Fatalf("a per-process capacity override was accepted: %+v, %v", turn, err)
	}
}

func TestAdmissionRejectsUnavailableMemory(t *testing.T) {
	for _, hasReader := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "failed"}[hasReader], func(t *testing.T) {
			// Arrange
			a := admission(t)
			a.Available = nil
			if hasReader {
				a.Available = func() (uint64, error) { return 0, errors.New("memory probe unavailable") }
			}

			// Act
			turn, err := a.Wait(context.Background())
			defer turn.Release()

			// Assert
			if err == nil || turn.release != nil {
				t.Fatalf("unavailable memory granted a turn: %+v, %v", turn, err)
			}
			if _, err := os.Stat(filepath.Join(a.Dir, "slot-1")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unavailable memory created a slot: %v", err)
			}
		})
	}
}

func TestAdmissionFloorDeadlineDoesNotGrantATurn(t *testing.T) {
	// Arrange
	a := admission(t)
	a.Available = func() (uint64, error) { return a.Floor - 1, nil }
	a.Limit = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Act
	turn, err := a.Wait(ctx)
	defer turn.Release()

	// Assert
	if err == nil || turn.release != nil || turn.Waited < a.Limit || ctx.Err() != nil {
		t.Fatalf("expired floor wait granted a turn or lost its wait: %+v, %v", turn, err)
	}
}

func TestAdmissionPreservesCorruptCustodyRegardlessOfAge(t *testing.T) {
	for _, area := range []string{"slot", "line"} {
		for name, data := range map[string][]byte{"empty": {}, "truncated": []byte(`{"pid":`), "missing_identity": []byte(`{}`)} {
			t.Run(area+"/"+name, func(t *testing.T) {
				// Arrange
				a := admission(t)
				file := filepath.Join(a.Dir, "slot-1")
				if area == "line" {
					file = filepath.Join(a.Dir, "line", "00000000000000000001-1-1")
					if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(file, data, 0o600); err != nil {
					t.Fatal(err)
				}
				old := time.Now().Add(-24 * time.Hour)
				if err := os.Chtimes(file, old, old); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()

				// Act
				turn, err := a.Wait(ctx)
				defer turn.Release()

				// Assert
				kept, readErr := os.ReadFile(file)
				if err == nil || turn.release != nil || readErr != nil || string(kept) != string(data) {
					t.Fatalf("uncertain custody was lost or admitted: turn=%+v err=%v kept=%q read=%v", turn, err, kept, readErr)
				}
			})
		}
	}
}

func TestAdmissionDoesNotTakeAnUnverifiableOverdueHolder(t *testing.T) {
	// Arrange
	a := admission(t)
	record := lock.Info{PID: 4242, OwnerPID: 4242, Hostname: "another-machine", Start: time.Now().Add(-3 * time.Hour), Acquired: time.Now().Add(-2 * time.Hour)}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(a.Dir, "slot-1")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Act
	turn, err := a.Wait(ctx)
	defer turn.Release()

	// Assert
	kept, readErr := os.ReadFile(file)
	if !errors.Is(err, context.DeadlineExceeded) || turn.release != nil || readErr != nil || string(kept) != string(data) {
		t.Fatalf("unverified live holder was displaced: turn=%+v err=%v read=%v", turn, err, readErr)
	}
}

func TestAnOldTurnCannotReleaseTheNextTurnOfTheSameProcess(t *testing.T) {
	// Arrange
	a := admission(t)
	old, err := a.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old.Release()
	current, err := a.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	before, err := os.ReadFile(filepath.Join(a.Dir, "slot-1"))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	old.Release()

	// Assert
	after, err := os.ReadFile(filepath.Join(a.Dir, "slot-1"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("an old release removed the current exact lease: %v", err)
	}
}

func TestAdmissionRejectsAnUnsupportedSharedCapacity(t *testing.T) {
	for name, data := range map[string]string{
		"zero": `{"capacity":0}`, "two": `{"capacity":2}`, "string": `{"capacity":"1"}`,
		"missing": `{}`, "unknown": `{"capacity":1,"other":1}`, "duplicate": `{"capacity":1,"capacity":1}`, "trailing": `{"capacity":1}{}`, "broken": `{`, "null": `{"capacity":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			a := admission(t)
			if err := os.WriteFile(filepath.Join(a.Dir, "capacity.json"), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			turn, err := a.Wait(context.Background())
			defer turn.Release()

			// Assert
			if err == nil || turn.release != nil {
				t.Fatalf("invalid shared capacity granted a turn: %+v, %v", turn, err)
			}
		})
	}
}

func TestAdmissionSharedCapacityOneAndStoreErrors(t *testing.T) {
	for _, setting := range []string{"absent", "one", "unreadable"} {
		t.Run(setting, func(t *testing.T) {
			// Arrange
			a := admission(t)
			path := filepath.Join(a.Dir, "capacity.json")
			if setting == "one" {
				if err := os.WriteFile(path, []byte(`{"capacity":1}`), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if setting == "unreadable" {
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			turn, err := a.Wait(context.Background())
			defer turn.Release()

			// Assert
			if setting == "unreadable" {
				if err == nil || turn.release != nil {
					t.Fatalf("unreadable capacity admitted: %+v %v", turn, err)
				}
			} else if err != nil || turn.release == nil {
				t.Fatalf("capacity one refused: %+v %v", turn, err)
			}
		})
	}
}

func TestAdmissionProductionUsesOneStoreDespiteProcessRedirects(t *testing.T) {
	// Arrange
	canonical, err := admissionDir("", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{"CFO_VERIFY_DIR", "LOCALAPPDATA", "XDG_CACHE_HOME", "HOME"} {
		t.Setenv(variable, filepath.Join(t.TempDir(), "another-pool"))
	}
	previous := os.Args[0]
	os.Args[0] = "cfo.exe"
	defer func() { os.Args[0] = previous }()

	// Act
	actual, err := AdmissionDir()
	explicit, explicitErr := admissionDir(canonical, false)

	// Assert
	if err != nil || explicitErr != nil || actual != canonical || explicit != canonical {
		t.Fatalf("process redirects changed the pool: canonical=%s actual=%s explicit=%s errors=%v/%v", canonical, actual, explicit, err, explicitErr)
	}
}

func TestAdmissionRequiresAnExplicitIsolatedTestStore(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", "")

	// Act
	_, cliErr := AdmissionDir()
	_, apiErr := admissionDir("", true)

	// Assert
	if cliErr == nil || apiErr == nil {
		t.Fatalf("test could reach the production store: cli=%v api=%v", cliErr, apiErr)
	}
}

func TestAdmissionPreservesCustodyOutsideSharedCapacity(t *testing.T) {
	// Arrange
	a := admission(t)
	data := []byte(`{"unknown":"legacy second slot"}`)
	path := filepath.Join(a.Dir, "slot-2")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	turn, err := a.Wait(context.Background())
	defer turn.Release()

	// Assert
	kept, readErr := os.ReadFile(path)
	if err == nil || turn.release != nil || readErr != nil || string(kept) != string(data) {
		t.Fatalf("outside-cap custody lost: %+v %v %v", turn, err, readErr)
	}
}

func TestAdmissionCancellationPreservesTheHolderAndOtherWaiters(t *testing.T) {
	// Arrange
	a := admission(t)
	holder, err := a.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	before, err := os.ReadFile(filepath.Join(a.Dir, "slot-1"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	joined := make(chan struct{}, 1)
	a.Who = "cancelled waiter"
	a.Waiting = func(time.Duration, string) {
		select {
		case joined <- struct{}{}:
		default:
		}
	}
	ended := make(chan error, 1)
	go func() { turn, err := a.Wait(ctx); turn.Release(); ended <- err }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not join")
	}
	var survivor said
	a.Who, a.Waiting = "surviving waiter", survivor.waiting
	waiting := take(t, a)
	survivor.waits(t, waiting, "the surviving waiter")

	// Act
	cancel()
	err = <-ended

	// Assert
	after, readErr := os.ReadFile(filepath.Join(a.Dir, "slot-1"))
	holding, inLine, lineErr := Line(a.Dir)
	if !errors.Is(err, context.Canceled) || readErr != nil || string(after) != string(before) || lineErr != nil || len(holding) != 1 || len(inLine) != 1 || inLine[0].Who != "surviving waiter" {
		t.Fatalf("cancellation changed other custody: err=%v read=%v holders=%+v waiters=%+v line=%v", err, readErr, holding, inLine, lineErr)
	}
	holder.Release()
	within(t, waiting, "the surviving waiter after release").Release()
}

func TestAdmissionKeepsCrossProcessFIFOAndCapacityOne(t *testing.T) {
	// Arrange
	a := admission(t)
	holder, err := a.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	type child struct {
		process *exec.Cmd
		stdin   io.WriteCloser
		output  <-chan string
	}
	var children []child
	t.Cleanup(func() {
		for _, child := range children {
			child.stdin.Close()
		}
		for _, child := range children {
			child.process.Wait()
		}
	})
	for index := range 2 {
		process := exec.Command(os.Args[0])
		process.Env = append(os.Environ(), "VERIFY_TEST_HOLD_DIR="+a.Dir)
		stdin, err := process.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := process.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		output := make(chan string, 1)
		go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); output <- strings.TrimSpace(line) }()
		children = append(children, child{process, stdin, output})
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			_, waiting, err := Line(a.Dir)
			if err == nil && len(waiting) == index+1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("child %d did not join: %+v %v", index, waiting, err)
			}
		}
	}

	// Act
	holder.Release()

	// Assert
	select {
	case line := <-children[0].output:
		if line != "holding" {
			t.Fatalf("first child: %s", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first child did not take its turn")
	}
	select {
	case line := <-children[1].output:
		t.Fatalf("second child overtook the first: %s", line)
	case <-time.After(250 * time.Millisecond):
	}
	holding, waiting, err := Line(a.Dir)
	if err != nil || len(holding) != 1 || holding[0].PID != children[0].process.Process.Pid || len(waiting) != 1 || waiting[0].PID != children[1].process.Process.Pid {
		t.Fatalf("cross-process capacity or FIFO changed: %+v %+v %v", holding, waiting, err)
	}
	children[0].stdin.Close()
	select {
	case line := <-children[1].output:
		if line != "holding" {
			t.Fatalf("second child: %s", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second child did not take its turn after release")
	}
	children[1].stdin.Close()
}
