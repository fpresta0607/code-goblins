package main

import (
	"fmt"
	"io"

	"github.com/fpresta0607/code-goblins/internal/voice"
)

func runVoiceWorker(arguments []string, input io.Reader, output, stderr io.Writer, open func(voice.WorkerOptions) (voice.WorkerRecognizer, error)) (exit int) {
	options, err := voice.ParseWorkerArguments(arguments)
	if err != nil {
		fmt.Fprintf(stderr, "voice-worker: %v\n", err)
		return 2
	}
	recognizer, err := open(options)
	if err != nil {
		fmt.Fprintf(stderr, "voice-worker: %v\n", err)
		return 1
	}
	defer func() {
		if err := recognizer.Close(); err != nil {
			fmt.Fprintf(stderr, "voice-worker: %v\n", err)
			exit = 1
		}
	}()
	if err := voice.RunWorker(input, output, recognizer.Recognize); err != nil {
		fmt.Fprintf(stderr, "voice-worker: %v\n", err)
		return 1
	}
	return 0
}
