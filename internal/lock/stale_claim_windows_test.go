package lock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestStaleClaimChild(t *testing.T) {
	dir := os.Getenv("LOCK_STALE_CLAIM_CHILD")
	if dir == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner, err := AcquireExclusiveNamedStrictContext(ctx, dir, "lease")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("holding")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	if err := ReleaseExclusiveOwned(ctx, dir, "lease", *owner); err != nil {
		t.Fatal(err)
	}
}

func TestStaleClaimPreservesCrossProcessReplacement(t *testing.T) {
	for _, isContext := range []bool{false, true} {
		t.Run(fmt.Sprintf("context=%t", isContext), func(t *testing.T) {
			// Arrange: both contenders read a dead identity before the child wins.
			dir, name := t.TempDir(), "lease"
			self, _ := ownerInfo(os.Getpid(), "parent")
			dead := *self
			dead.Start = dead.Start.Add(-time.Hour)
			if dead.Alive() {
				t.Fatal("fixture identity is not dead")
			}
			if err := writeInfo(filepath.Join(dir, name), &dead); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			t.Cleanup(cancel)
			child := execx.CommandContext(ctx, os.Args[0], "-test.run=^TestStaleClaimChild$")
			child.Env = append(os.Environ(), "LOCK_STALE_CLAIM_CHILD="+dir)
			stdin, err := child.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			child.Stderr = os.Stderr
			reads := 0
			read := func(dir, name string) (*Info, error) {
				holder, err := ReadNamedStrict(dir, name)
				reads++
				if reads == 2 {
					if err := child.Start(); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						stdin.Close()
						if err := child.Wait(); err != nil {
							t.Error(err)
						}
					})
					line, err := bufio.NewReader(stdout).ReadString('\n')
					if err != nil || line != "holding\n" {
						t.Fatalf("child did not acquire: %q %v", line, err)
					}
				}
				return holder, err
			}

			// Act: the parent resumes with its stale read after the child created a live lease.
			if isContext {
				_, err = acquireExclusiveStrictReading(ctx, dir, name, func(_ context.Context, dir, name string) (*Info, error) { return read(dir, name) })
			} else {
				_, err = acquireReading(dir, name, self, false, true, read)
			}

			// Assert: the child's lease survives and the parent is refused.
			if !errors.Is(err, ErrHeld) {
				t.Errorf("stale contender acquired over live child: %v", err)
			}
			current, err := ReadNamedStrict(dir, name)
			if err != nil || current.PID != child.Process.Pid {
				t.Fatalf("replacement lost: %+v %v", current, err)
			}
			if _, err := AcquireExclusiveNamedStrictContext(ctx, dir, name); !errors.Is(err, ErrHeld) {
				t.Fatalf("live foreign negative control acquired: %v", err)
			}
		})
	}
}

func TestContextAcquisitionVerifiesCreatedIdentity(t *testing.T) {
	dir, name := t.TempDir(), "lease"
	var replacement Info
	owner, err := acquireExclusiveStrictReading(context.Background(), dir, name, func(ctx context.Context, dir, name string) (*Info, error) {
		current, err := ReadNamedStrictContext(ctx, dir, name)
		if err != nil {
			return nil, err
		}
		replacement = *current
		replacement.Acquired = replacement.Acquired.Add(time.Second)
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		if err := writeInfo(filepath.Join(dir, name), &replacement); err != nil {
			t.Fatal(err)
		}
		return &replacement, nil
	})
	if err == nil || owner != nil {
		t.Fatalf("reported ownership after replacement: %+v %v", owner, err)
	}
	if _, err := AcquireExclusiveNamedStrictContext(context.Background(), dir, name); !errors.Is(err, ErrHeld) {
		t.Fatal(err)
	}
	current, err := ReadNamedStrict(dir, name)
	if err != nil || *current != replacement {
		t.Fatalf("replacement changed: %+v %v", current, err)
	}
}
