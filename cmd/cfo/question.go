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
	forTask := f.String("for", "", "the goblin's task ID when this puts that goblin's decision to the user; while this question waits, that goblin's waits on the user must declare whether they are this decision, and one it declares with cfo notify --follows <this question's ID> is hidden while this question waits and closed by its answer")
	var options []string
	f.Func("option", "one real choice; repeat for each choice; omit for written answers", func(value string) error { options = append(options, value); return nil })
	if !parseFlags(f, args) {
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
	// AFK mode is complete autopilot: while it is on nothing is asked of the
	// Overlord. The CFO answers it, and a step only he can take is a line of
	// his Command Center with a backlog row.
	if switched, err := afk.Read(h.State); err == nil && switched.On {
		fmt.Fprintln(stderr, "cfo question: AFK mode is on, so nothing is asked of the Overlord. Answer it yourself with your recommendation, act on it and log it with cfo afk log --kind left ... --answer \"<your choice>\". If it is a step only he can take, such as his own sign-in or a step a tool refuses, give it a backlog row in data/backlog.md, log it with cfo afk log --kind his --what \"<the step he must take>\" --evidence \"<why you cannot take it, and its backlog row>\", and work around it.")
		return 2
	}
	c := supervisor.CFOConnection{State: h.State}
	if err := c.PublishQuestion(*id, *text, options, *recommended, *forTask); err != nil {
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
