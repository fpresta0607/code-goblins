package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// runSwitch changes a running goblin's harness, model, or effort in place.
func runSwitch(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "goblins switch: task ID is required")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("switch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	harnessName := flags.String("harness", "", "claude, codex, or pi")
	model := flags.String("model", "", "model for the new harness")
	effort := flags.String("effort", "", "reasoning effort for the new harness")
	generation := flags.String("generation", "", "switch only the selected task session")
	forceDirty := flags.Bool("force-dirty", false, "switch even though the worktree has uncommitted changes")
	restart := flags.Bool("restart", false, "start the goblin's own harness, model and effort again on its conversation, onto any update of its harness")
	renamed := flags.Bool("renamed", false, "with --restart: start it again under goblins' names, as the supervisor does after its home moved to them")
	isAfterAPIError := flags.Bool("after-api-error", false, "with --restart: start it again after its turn ended on an API error, as the supervisor does once the API answers and its terminal takes no typing")
	inTerminal := flags.Bool("terminal", false, "start the goblin again on its conversation in a terminal, taking a goblin on stream-json pipes over; --restart hands it back")
	if !parseFlags(flags, args[1:]) {
		return 2
	}
	if *inTerminal && *renamed {
		fmt.Fprintln(stderr, "goblins switch: --renamed goes with --restart, not --terminal")
		return 2
	}
	if *inTerminal && (*restart || *harnessName != "" || *model != "" || *effort != "") {
		fmt.Fprintln(stderr, "goblins switch: --terminal keeps the goblin's own harness, model and effort; leave out --restart, --harness, --model and --effort")
		return 2
	}
	if *isAfterAPIError && (!*restart || *renamed) {
		fmt.Fprintln(stderr, "goblins switch: --after-api-error goes with --restart, not --renamed or --terminal")
		return 2
	}
	*restart = *restart || *inTerminal
	if *renamed && !*restart {
		fmt.Fprintln(stderr, "goblins switch: --renamed goes with --restart")
		return 2
	}
	if *restart && (*harnessName != "" || *model != "" || *effort != "") {
		fmt.Fprintln(stderr, "goblins switch: --restart keeps the goblin's own harness, model and effort; leave out --harness, --model and --effort")
		return 2
	}
	if !*restart && *harnessName == "" && *model == "" && *effort == "" {
		fmt.Fprintln(stderr, "goblins switch: one of --harness, --model, --effort, --restart or --terminal is required")
		return 2
	}
	if *harnessName != "" && !validSpawnHarness(*harnessName) {
		fmt.Fprintln(stderr, "goblins switch: --harness must be claude, codex, or pi")
		return 2
	}

	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// A handed-off goblin's branch is the cloud session's to build on, and a
	// switch relaunches a paused goblin outside its lifecycle.
	record, err := state.ReadLifecycle(h.State, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err == nil && record.Generation == meta.SpawnGen && record.IsHandedOff() {
		fmt.Fprintln(stderr, "goblins switch: the task is handed off to a cloud session: goblins handoff return brings its work back and resumes it, and goblins handoff cancel resumes it without that work")
		return 1
	}
	// The switch is pinned to the session the caller chose or, with none
	// named, the one read here, so a task replaced in between is refused.
	pinned := *generation
	if pinned == "" {
		pinned = meta.SpawnGen
	}
	request := spawn.SwitchRequest{
		ID:         id,
		Harness:    harness.Kind(*harnessName),
		Model:      *model,
		Effort:     *effort,
		Generation: pinned,
		ForceDirty: *forceDirty,
		Restart:    *restart,
		InTerminal: *inTerminal,
		BriefPath:  filepath.Join(h.Data, id, "brief.md"),
	}
	switch {
	case *inTerminal:
		request.ResumeNote = "The Overlord took you over in a terminal, on your own conversation, harness, model and effort."
	case *restart:
		request.ResumeNote = "The Overlord restarted you on your own harness, model and effort."
	}
	if *renamed {
		request.ResumeNote = renamedNote
	}
	if *isAfterAPIError {
		request.ResumeNote = apiErrorRestartNote
	}
	if request.Harness == "" || string(request.Harness) == meta.Harness {
		if request.ResumeSession, err = taskSession(h.State, meta); err != nil {
			fmt.Fprintf(stderr, "switch: %v\n", err)
			return 1
		}
	}
	result, err := runtime.switchTask(context.Background(), h, request)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, result.Output)
	if hint := runtime.speedHint(context.Background(), result.Meta.Harness); hint != "" {
		fmt.Fprintln(stdout, hint)
	}
	return 0
}

// renamedNote is what a goblin restarted under goblins' names is told, the
// one line it needs: the names it used before this update are gone.
const renamedNote = "Code Goblins updated, and its command is now goblins and its settings begin GOBLINS_: the command and settings you used before are gone, so run goblins from here on. You were restarted under the new names at the end of your turn, on your own conversation, harness, model and effort."

// apiErrorRestartNote is what a goblin the supervisor restarted after its
// turn ended on an API error is told: who restarted it, and why.
const apiErrorRestartNote = "The supervisor restarted you on your own conversation, harness, model and effort: your last turn ended on an API error, and once the API answered again your terminal took no typing. Carry on from where you stopped."

// taskSession is the conversation the task's goblin of its current
// generation is in: the one the board recorded or kept as its running
// harness proved it (fleettree.OwnedSession), or else the one its harness,
// while it runs, records itself.
func taskSession(stateDir string, meta state.TaskMeta) (string, error) {
	session, err := fleettree.OwnedSession(stateDir, meta)
	if err != nil || session != "" {
		return session, err
	}
	return runningSession(stateDir, meta), nil
}

// runningSession is the conversation the task's running harness is in, as
// the harness records it itself, for a goblin the board recorded none for,
// as a home without the native session hooks records none: the switch
// resumes it rather than starting the goblin cold.
func runningSession(stateDir string, meta state.TaskMeta) string {
	record, err := host.ReadRecord(stateDir, meta.ID)
	if err != nil || !host.Running(record) {
		return ""
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	reader := fleettree.Reader{Home: userHome}
	return reader.RunningSession(context.Background(), fleettree.Goblin{Meta: meta, HarnessPID: record.ChildPID, HarnessStarted: record.ChildStart})
}
