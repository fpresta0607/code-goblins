package main

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/fpresta0607/code-goblins/internal/install"
)

// nativeHookLimit is how long a native session hook may run, and
// keyAlarmLimit how long the key alarm may: each the limit Claude Code held
// it to as a hook entry of its own.
const (
	nativeHookLimit = install.NativeHookSeconds * time.Second
	keyAlarmLimit   = install.KeyAlarmSeconds * time.Second
)

// hookPart is one hook of a hook entry that runs several, with the time
// limit it had as an entry of its own.
type hookPart struct {
	name  string
	limit time.Duration
	run   func(stdout, stderr io.Writer) int
}

// runHookParts runs parts at once, each writing output of its own, and
// waits for each up to its own limit, counted from the start, then writes
// what each wrote, in order. It exits with the first exit code that is not
// zero. A part still running at its limit is left unfinished and exits 1, as
// Claude Code ended a hook entry past its limit: the process exits once each
// part has finished or run out its limit, which ends one left running.
func runHookParts(stdout, stderr io.Writer, parts ...hookPart) int {
	type outcome struct {
		code        int
		out, errors bytes.Buffer
		done        chan struct{}
	}
	started := time.Now()
	outcomes := make([]*outcome, len(parts))
	for index, part := range parts {
		result := &outcome{done: make(chan struct{})}
		outcomes[index] = result
		go func() {
			defer close(result.done)
			result.code = part.run(&result.out, &result.errors)
		}()
	}
	exit := 0
	for index, part := range parts {
		result := outcomes[index]
		limit := time.NewTimer(time.Until(started.Add(part.limit)))
		select {
		case <-result.done:
			limit.Stop()
			_, _ = stdout.Write(result.out.Bytes())
			_, _ = stderr.Write(result.errors.Bytes())
			if exit == 0 {
				exit = result.code
			}
		case <-limit.C:
			fmt.Fprintf(stderr, "%s did not finish within %s\n", part.name, part.limit)
			if exit == 0 {
				exit = 1
			}
		}
	}
	return exit
}
