//go:build windows || linux || darwin

package main

import (
	"fmt"
	"io"
	"sync/atomic"

	"github.com/fpresta0607/code-goblins/internal/host"
)

// nativeRecord is the record of native terminal id, or false, said on stderr,
// when none runs or it runs on stream-json pipes with no terminal to show.
func nativeRecord(stateDir, id string, stderr io.Writer) (host.Record, bool) {
	record, err := host.ReadRecord(stateDir, id)
	if err != nil {
		fmt.Fprintf(stderr, "goblins attach: no native terminal %s is running: %v\n", id, err)
		return host.Record{}, false
	}
	if record.Stream {
		fmt.Fprintf(stderr, "goblins attach: %s runs on stream-json pipes, with no terminal to show; goblins switch %s --terminal takes it over in one, and goblins peek %s shows what it is doing\n", id, id, id)
		return host.Record{}, false
	}
	return record, true
}

// viewRecord connects to native terminal id's host as a viewer, or returns
// false, said on stderr, when it does not answer.
func viewRecord(record host.Record, id string, stderr io.Writer) (*host.Client, bool) {
	client, err := host.View(record)
	if err != nil {
		fmt.Fprintf(stderr, "goblins attach: native terminal %s does not answer: %v\n", id, err)
		return nil, false
	}
	return client, true
}

// viewNative shows the terminal client views on stdout and sends it the keys
// read from keys, until the terminal ends, which returns its exit code, or
// Ctrl-] is pressed, which leaves it running and returns 0. size is this
// console's size, 0 by 0 when it cannot be read, which the terminal takes at
// the start and on each sizeChanged; when another viewer, such as the board,
// gave the terminal another size, the next key typed takes it back first.
func viewNative[T any](client *host.Client, id string, keys io.Reader, stdout io.Writer, size func() (int, int), sizeChanged <-chan T) int {
	cols, rows := size()
	_ = client.Resize(cols, rows)
	// isOwner is whether the terminal has this console's size, as far as the
	// sizes the host told say.
	var isOwner atomic.Bool
	isOwner.Store(true)

	type ending struct {
		reason string
		code   int
	}
	ended := make(chan ending, 2)
	go func() {
		for {
			event, err := client.Next()
			switch {
			case err != nil:
				ended <- ending{"The native terminal's host stopped answering.", 1}
				return
			case event.Exited:
				ended <- ending{fmt.Sprintf("The native terminal ended with exit code %d.", event.Code), int(event.Code)}
				return
			case event.Cols > 0:
				c, r := size()
				isOwner.Store(event.Cols == c && event.Rows == r)
				continue
			}
			_, _ = stdout.Write(event.Output)
		}
	}()
	go func() {
		typedKeys := make([]byte, 4096)
		for {
			n, err := keys.Read(typedKeys)
			if err != nil {
				ended <- ending{"This console stopped reading keys; native terminal " + id + " keeps running.", 1}
				return
			}
			typed := typedKeys[:n]
			left := false
			if at := detachAt(typed); at >= 0 {
				typed, left = typed[:at], true
			}
			if len(typed) > 0 && !isOwner.Load() {
				if c, r := size(); c > 0 {
					isOwner.Store(true)
					_ = client.Resize(c, r)
				}
			}
			if len(typed) > 0 && client.Input(typed) != nil {
				ended <- ending{"The native terminal's host stopped answering.", 1}
				return
			}
			if left {
				ended <- ending{"Left native terminal " + id + " running; goblins attach brings it back.", 0}
				return
			}
		}
	}()
	for {
		select {
		case end := <-ended:
			fmt.Fprintf(stdout, "\r\n%s\r\n", end.reason)
			_, _ = io.WriteString(stdout, terminalReset)
			return end.code
		case <-sizeChanged:
			if c, r := size(); c > 0 && (c != cols || r != rows) {
				cols, rows = c, r
				_ = client.Resize(c, r)
			}
		}
	}
}
