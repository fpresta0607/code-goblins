package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/voice"
)

// runDictation is `cfo dictation setup`, which the install runs so dictation
// works at the first press: it downloads the speech engine and model this
// home pins, keeps each only when it matches its pinned SHA-256, puts them
// where dictation looks, and removes an engine or model an earlier pin left.
// What is there already is not downloaded again.
func runDictation(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) != 1 || args[0] != "setup" {
		fmt.Fprintln(stderr, "usage: cfo dictation setup")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	speech, err := voice.For(h.Root, codegoblins.Voice)
	if err != nil {
		fmt.Fprintf(stderr, "dictation: settings unreadable: %v\n", err)
		return 1
	}
	speech.Client = runtime.dictationClient
	name := fmt.Sprintf("%s %s on %s %s", speech.Settings.Model.Name, speech.Settings.Model.Version, speech.Settings.Engine.Name, speech.Settings.Engine.Version)
	absent := speech.Absent()
	if len(absent) > 0 {
		fmt.Fprintf(stdout, "dictation: downloading %s, %d MB, into %s\n", partNames(absent), speech.Missing()>>20, speech.Dir)
	}
	tenths := int64(0)
	err = speech.Fetch(context.Background(), func(done, total int64) {
		if now := done * 10 / total; now > tenths {
			tenths = now
			fmt.Fprintf(stdout, "dictation: %d of %d MB\n", done>>20, total>>20)
		}
	})
	if err != nil {
		fmt.Fprintf(stderr, "dictation: %v\n", err)
		return 1
	}
	if len(absent) == 0 {
		fmt.Fprintf(stdout, "dictation: ready: %s, already in %s, so nothing was downloaded\n", name, speech.Dir)
		return 0
	}
	fmt.Fprintf(stdout, "dictation: ready: %s, in %s\n", name, speech.Dir)
	return 0
}

// partNames names parts as their name and version, joined with and.
func partNames(parts []voice.Part) string {
	names := make([]string, len(parts))
	for i, part := range parts {
		names[i] = part.Name + " " + part.Version
	}
	return strings.Join(names, " and ")
}
