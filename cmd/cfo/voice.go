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
	if err := voice.RunWorker(input, output, recognize); err != nil {
		fmt.Fprintf(stderr, "voice-worker: %v\n", err)
		return 1
	}
	return 0
}
