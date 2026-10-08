package main

import (
	"fmt"
	"io"

	"github.com/fpresta0607/code-goblins/internal/voice"
)

// runVoiceWorker is the hidden `cfo voice-worker`, which cfo serve starts to
// keep dictation's engine loaded between dictations: it loads the engine once
// and recognises each sound its standard input brings until that closes.
func runVoiceWorker(arguments []string, input io.Reader, output, stderr io.Writer) int {
	// The worker runs hidden, as a child of a board often started at login
	// with no window, which Windows throttles onto slow scheduling: a loaded
	// engine answered in 4 to 5 s instead of 0.2 s. It asks for the
	// scheduling cfo serve asks for; without it dictation is only slower.
	if err := serveScheduling(); err != nil {
		fmt.Fprintf(stderr, "voice-worker: %v\n", err)
	}
	options, err := voice.ParseWorkerArguments(arguments)
	if err != nil {
		fmt.Fprintf(stderr, "voice-worker: %v\n", err)
		return 2
	}
	recognize, err := voice.OpenWorker(options)
	if err != nil {
		fmt.Fprintf(stderr, "voice-worker: %v\n", err)
		return 1
	}
	if err := voice.RunWorker(input, output, stderr, recognize); err != nil {
		fmt.Fprintf(stderr, "voice-worker: %v\n", err)
		return 1
	}
	return 0
}
