package fleettree

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestMessagePageContextCancelsFileOpenWait(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, isDeadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deadline=%t", harness, isDeadline), func(t *testing.T) {
				reader, path := contextMessageFixture(t, harness)
				name, err := windows.UTF16PtrFromString(path)
				if err != nil {
					t.Fatal(err)
				}
				handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
				if err != nil {
					t.Fatal(err)
				}
				isOpen := true
				defer func() {
					if isOpen {
						windows.CloseHandle(handle)
					}
				}()
				ctx, cancel := context.WithCancel(context.Background())
				if isDeadline {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond) // stall-accepted: the test means this context to run out.
				}
				defer cancel()
				finished := make(chan error, 1)
				go func() {
					_, _, err := reader.MessagePageContext(ctx, path, -1)
					finished <- err
				}()
				if !isDeadline {
					timer := time.AfterFunc(30*time.Millisecond, cancel) // stall-accepted: it only makes the cancel come later.
					defer timer.Stop()
				}

				want := context.Canceled
				if isDeadline {
					want = context.DeadlineExceeded
				}
				select {
				case err := <-finished:
					if !errors.Is(err, want) {
						t.Fatalf("held-file lookup returned %v, want %v", err, want)
					}
				case <-time.After(time.Minute):
					t.Fatal("lookup waited for the held file after cancellation")
				}
				if err := windows.CloseHandle(handle); err != nil {
					t.Fatal(err)
				}
				isOpen = false

				checkContextMessageHistory(t, reader, path, 120)
			})
		}
	}
}
