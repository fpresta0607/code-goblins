package lock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// publishWriters are the two ways a holder record is written: the strict
// exclusive acquisition a custody change takes, and the shared acquire loop
// an admission slot is taken through.
var publishWriters = map[string]func(ctx context.Context, dir, name string) (*Info, error){
	"strict context": AcquireExclusiveNamedStrictContext,
	"strict": func(_ context.Context, dir, name string) (*Info, error) {
		return AcquireExclusiveNamedStrict(dir, name)
	},
}

func TestPublishingChild(t *testing.T) {
	dir := os.Getenv("LOCK_PUBLISHING_CHILD")
	if dir == "" {
		return
	}
	publishing = func() {
		fmt.Println("publishing")
		time.Sleep(time.Hour)
	}
	if _, err := publishWriters[os.Getenv("LOCK_PUBLISHING_WRITER")](context.Background(), dir, "custody-mutation"); err != nil {
		t.Fatal(err)
	}
}

// A run that is ended while it writes its holder record, as a test's cleanup
// and cfo kill end a run waiting in line, left the record created and empty.
// Every later strict acquisition refused it as an unreadable holder record,
// so no custody change could happen in that line again (#1665). A record
// being written is not there for a reader, and a writer ended part way
// leaves no record.
func TestAHolderRecordIsNeverSeenHalfWritten(t *testing.T) {
	for writer, acquire := range publishWriters {
		t.Run(writer, func(t *testing.T) {
			// Arrange: a child process stops while it writes the record.
			dir, name := t.TempDir(), "custody-mutation"
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			t.Cleanup(cancel)
			child := execx.CommandContext(ctx, os.Args[0], "-test.run=^TestPublishingChild$")
			child.Env = append(os.Environ(), "LOCK_PUBLISHING_CHILD="+dir, "LOCK_PUBLISHING_WRITER="+writer)
			child.Stderr = os.Stderr
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = child.Process.Kill()
				_ = child.Wait()
			})
			if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "publishing\n" {
				t.Fatalf("child did not start writing: %q %v", line, err)
			}

			// Act: read the record while it is written, then end its writer.
			reading, cancelReading := context.WithTimeout(ctx, time.Second)
			defer cancelReading()
			_, readErr := ReadNamedStrictContext(reading, dir, name)
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = child.Wait()
			owner, acquireErr := acquire(ctx, dir, name)

			// Assert
			if !errors.Is(readErr, os.ErrNotExist) {
				t.Errorf("a read while the record is written = %v, want no record", readErr)
			}
			if acquireErr != nil {
				t.Fatalf("acquiring after the writer ended part way = %v, want the lock", acquireErr)
			}
			if err := ReleaseExclusiveOwned(ctx, dir, name, *owner); err != nil {
				t.Error(err)
			}
		})
	}
}
