// Command consumer is the detached controller used by the janitor fixture.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	store := os.Getenv("CFO_VERIFY_DIR")
	if os.Getenv("CFO_CONSUMER_PHASE") == "orphan" {
		if err := fsx.AtomicWriteFile(filepath.Join(store, "ready"), nil); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}
	dir, err := verify.AdmissionDir()
	if err != nil {
		return err
	}
	var waitingError error
	executor := verify.Executor{
		Admission: verify.Admission{Dir: dir, Who: "isolated borrowed controller fixture", Limit: time.Minute, Poll: 20 * time.Millisecond, Waiting: func(_ time.Duration, why string) {
			waitingError = fsx.AtomicWriteFile(filepath.Join(store, "waiting"), []byte(why))
			if waitingError != nil {
				cancel()
			}
		}},
		Monitor: verify.Monitor{Headroom: func() (verify.Headroom, error) {
			var memory verify.Headroom
			data, err := fsx.ReadFile(filepath.Join(store, "headroom.json"))
			if err != nil {
				return memory, err
			}
			err = json.Unmarshal(data, &memory)
			return memory, err
		}},
		ExecutionLimit: 3 * time.Minute,
	}
	_, err = executor.Run(ctx, execx.Request{Dir: store, Name: filepath.Join(store, "held.exe")})
	return errors.Join(err, waitingError)
}
