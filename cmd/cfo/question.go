package main

import (
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func runQuestion(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("question", flag.ContinueOnError)
	f.SetOutput(stderr)
	id := f.String("id", "", "stable ID for this CFO question")
	text := f.String("text", "", "the question deliberately escalated to the user: one short sentence that is the question, details on lines starting with \"- \", and **bold** only on the verdict or the blocking item")
	recommended := f.String("recommend", "", "exact supplied choice to recommend; omit when there is no recommendation")
	var options []string
	f.Func("option", "one real choice; repeat for each choice; omit for written answers", func(value string) error { options = append(options, value); return nil })
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return 2
	}
	if err := choicesAreAnswers(options); err != nil {
		fmt.Fprintln(stderr, "cfo question: "+err.Error())
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// While AFK mode is on, what the CFO would have asked the Overlord is held
	// for him with the choice it would make.
	if switched, err := afk.Read(h.State); err == nil && switched.On && len(options) > 0 && *recommended == "" {
		fmt.Fprintln(stderr, "cfo question: AFK mode is on, so this is held for the Overlord under Held for you, never asked, and it carries your recommendation: pass --recommend with the choice you would make")
		return 2
	}
	c := supervisor.CFOConnection{State: h.State}
	if err := c.PublishQuestion(*id, *text, options, *recommended); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "User question published:", *id)
	return 0
}

// A letter or number alone, as in a, B), (c) or 2.
var bareChoice = regexp.MustCompile(`^\(?([A-Za-z]|[0-9]+)[).:]?$`)

// choicesAreAnswers refuses a choice that names no answer. The Overlord picks
// from a plain list of the answers themselves, so a choice that is only a
// letter or number tells him nothing; a goblin's "(Recommended)" mark is not
// part of the answer.
func choicesAreAnswers(options []string) error {
	for _, option := range options {
		choice, _ := strings.CutSuffix(strings.TrimSpace(option), "(Recommended)")
		if bareChoice.MatchString(strings.TrimSpace(choice)) {
			return fmt.Errorf("choice %q is only a letter or number; write the answer itself as the choice, a short phrase such as Fix it next or Keep 300 s, and keep details in the question's \"- \" lines", strings.TrimSpace(option))
		}
	}
	return nil
}
