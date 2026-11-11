package fleettree

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// modelCommand is Claude Code's record of /model typed with args, and
// commandPrinted what it printed for the command before it.
func modelCommand(uuid, args string) object {
	return prompted(uuid, "<command-name>/model</command-name>\n            <command-message>model</command-message>\n            <command-args>"+args+"</command-args>")
}

func effortCommand(uuid, args string) object {
	return prompted(uuid, "<command-name>/effort</command-name>\n            <command-message>effort</command-message>\n            <command-args>"+args+"</command-args>")
}

func commandPrinted(uuid, printed string) object {
	return prompted(uuid, "<local-command-stdout>"+printed+"</local-command-stdout>")
}

// ranCommand is Claude Code 2.1.312's record of a command it ran itself with
// args typed after it, and ranPrinted of what that command printed: a system
// local_command record each, with no user message.
func ranCommand(uuid, command, args string) object {
	return system(uuid, "local_command", object{"content": "<command-name>/" + command + "</command-name>\n            <command-message>" + command + "</command-message>\n            <command-args>" + args + "</command-args>", "level": "info", "isMeta": false})
}

func ranPrinted(uuid, command, args, printed string) object {
	return system(uuid, "local_command", object{"content": "<local-command-stdout>" + printed + "</local-command-stdout>", "level": "info", "isMeta": false, "commandRun": object{"command": command, "args": args}})
}

// ranOn is an answer of model at effort.
func ranOn(uuid, id, model, effort string) object {
	ran := answer(uuid, id, textBlock("done"))
	ran["message"].(object)["model"] = model
	if effort != "" {
		ran["effort"] = effort
	}
	return ran
}

// writtenAt gives entry the time it was written.
func writtenAt(entry object, when time.Time) object {
	entry["timestamp"] = when.Format(time.RFC3339Nano)
	return entry
}

// A session's engine is what its harness's own record last names: Claude
// Code's answers, which name the model and the effort they ran at, its /model
// and /effort results, Codex's turn context, and pi's model and thinking
// level changes (the Overlord, 2026-11-10: "It all should be one standard
// setting, centralized control, just different views").
func TestASessionReadsTheEngineItsHarnessLastNamed(t *testing.T) {
	for _, test := range []struct {
		name    string
		harness string
		path    func(t *testing.T, home string) string
		want    Engine
	}{
		{"Claude Code's answer names the model and the effort it ran at", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("u1", "work"), ranOn("a1", "reply-1", "claude-fable-5-2", "xhigh"))
		}, Engine{Model: "claude-fable-5-2", ModelAt: at, Effort: "xhigh", EffortAt: at}},
		{"Claude Code's /model with a model typed after it, before any answer of the new model", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("u1", "work"), ranOn("a1", "reply-1", "claude-opus-5-5", "high"),
				writtenAt(modelCommand("c1", "claude-fable-5-2[1m]"), at.Add(time.Minute)),
				writtenAt(commandPrinted("o1", "Set model to `Fable 5.2` and saved as your default for new sessions"), at.Add(time.Minute)))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at, ModelTyped: "claude-fable-5-2[1m]", ModelTypedAt: at.Add(time.Minute), Effort: "high", EffortAt: at}},
		{"Claude Code's /model picker, which names the model it kept", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("u1", "work"),
				writtenAt(modelCommand("c1", ""), at.Add(time.Minute)),
				writtenAt(commandPrinted("o1", "Kept model as `Fable 5.2`"), at.Add(time.Minute)))
		}, Engine{ModelName: "Fable 5.2", ModelAt: at.Add(time.Minute)}},
		{"an alias and a context window typed after Claude Code's /model outlive an answer on that model", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, modelCommand("c1", "opus[1m]"), commandPrinted("o1", "Set model to `Opus 5.5 (1M context)`"),
				writtenAt(ranOn("a1", "reply-1", "claude-opus-5-5", "high"), at.Add(time.Minute)))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at.Add(time.Minute), ModelTyped: "opus[1m]", ModelTypedAt: at, Effort: "high", EffortAt: at.Add(time.Minute)}},
		{"an id typed after Claude Code's /model outlives an answer that names it", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, modelCommand("c1", "claude-opus-5-5"), commandPrinted("o1", "Set model to `Opus 5.5`"),
				writtenAt(ranOn("a1", "reply-1", "claude-opus-5-5", ""), at.Add(time.Minute)))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at.Add(time.Minute), ModelTyped: "claude-opus-5-5", ModelTypedAt: at}},
		{"a model typed after Claude Code's /model gives way to the one its picker sets", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, modelCommand("c1", "opus"), commandPrinted("o1", "Set model to `Opus 5.5`"),
				writtenAt(modelCommand("c2", ""), at.Add(time.Minute)), writtenAt(commandPrinted("o2", "Set model to `Fable 5.2`"), at.Add(time.Minute)))
		}, Engine{ModelName: "Fable 5.2", ModelAt: at.Add(time.Minute)}},
		{"an alias typed after Claude Code's /model gives way to an answer on another model", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, modelCommand("c1", "opus"), commandPrinted("o1", "Set model to `Opus 5.5`"),
				writtenAt(ranOn("a1", "reply-1", "claude-sonnet-5-5", ""), at.Add(time.Minute)))
		}, Engine{Model: "claude-sonnet-5-5", ModelAt: at.Add(time.Minute)}},
		{"an id typed after Claude Code's /model is no alias of a newer model's", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, modelCommand("c1", "claude-opus-5"), commandPrinted("o1", "Set model to `Opus 5`"),
				writtenAt(ranOn("a1", "reply-1", "claude-opus-5-5", ""), at.Add(time.Minute)))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at.Add(time.Minute)}},
		{"Claude Code's cancelled /model", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, ranOn("a1", "reply-1", "claude-opus-5-5", ""), writtenAt(modelCommand("c1", ""), at.Add(time.Minute)), writtenAt(commandPrinted("o1", "Cancelled"), at.Add(time.Minute)))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at}},
		{"Claude Code's answer after /model names the model by its id", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, modelCommand("c1", ""), commandPrinted("o1", "Set model to `Fable 5.2`"),
				writtenAt(ranOn("a1", "reply-1", "claude-fable-5-2", "high"), at.Add(time.Minute)))
		}, Engine{Model: "claude-fable-5-2", ModelAt: at.Add(time.Minute), Effort: "high", EffortAt: at.Add(time.Minute)}},
		{"words that only look like /model's result set nothing", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, ranOn("a1", "reply-1", "claude-opus-5-5", ""), prompted("u2", "work"), commandPrinted("o1", "Set model to `Fable 5.2`"))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at}},
		{"Claude Code's usage limit notice names no model", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, ranOn("a1", "reply-1", "claude-opus-5-5", "high"), writtenAt(ranOn("a2", "reply-2", "<synthetic>", ""), at.Add(time.Minute)))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at, Effort: "high", EffortAt: at}},
		{"a sub-agent's answer in the conversation is not the session's", "claude", func(t *testing.T, home string) string {
			side := ranOn("a2", "reply-2", "claude-haiku-5-5", "low")
			side["isSidechain"] = true
			return conversation(t, home, ranOn("a1", "reply-1", "claude-opus-5-5", "high"), writtenAt(side, at.Add(time.Minute)))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at, Effort: "high", EffortAt: at}},
		{"Claude Code's /effort with a level", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, ranOn("a1", "reply-1", "claude-opus-5-5", "high"), writtenAt(effortCommand("c1", "max"), at.Add(time.Minute)), writtenAt(commandPrinted("o1", "Set effort level to max"), at.Add(time.Minute)))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at, Effort: "max", EffortAt: at.Add(time.Minute)}},
		{"Claude Code's /effort picker", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, effortCommand("c1", ""), commandPrinted("o1", "Set effort level to xhigh (this session only)"))
		}, Engine{Effort: "xhigh", EffortAt: at}},
		{"Claude Code's /effort with its description", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, effortCommand("c1", "high"), commandPrinted("o1", "Set effort level to high: Deeper reasoning for harder problems"))
		}, Engine{Effort: "high", EffortAt: at}},
		{"Claude Code's /effort auto", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, effortCommand("c1", "auto"), commandPrinted("o1", "Effort level set to auto"))
		}, Engine{Effort: "auto", EffortAt: at}},
		{"Claude Code's cancelled /effort", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, effortCommand("c1", ""), commandPrinted("o1", "Cancelled"))
		}, Engine{}},
		{"Claude Code's refused /effort, which lists every level", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, effortCommand("c1", "hgih"), commandPrinted("o1", "Invalid argument: hgih. Valid options are: low, medium, high, xhigh, max, auto"))
		}, Engine{}},
		{"Claude Code's latest answer, after /effort", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, effortCommand("c1", "max"), commandPrinted("o1", "Set effort level to max"), writtenAt(ranOn("a2", "reply-2", "claude-opus-5-5", "low"), at.Add(time.Minute)))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at.Add(time.Minute), Effort: "low", EffortAt: at.Add(time.Minute)}},
		{"Claude Code's /model with a model typed after it, recorded as system entries", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("u1", "work"), ranOn("a1", "reply-1", "claude-opus-5-5", "high"),
				writtenAt(ranCommand("c1", "model", "claude-fable-5-2[1m]"), at.Add(time.Minute)),
				writtenAt(ranPrinted("o1", "model", "claude-fable-5-2[1m]", "Set model to `Fable 5.2` and saved as your default for new sessions"), at.Add(time.Minute)))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at, ModelTyped: "claude-fable-5-2[1m]", ModelTypedAt: at.Add(time.Minute), Effort: "high", EffortAt: at}},
		{"Claude Code's /model picker, recorded as system entries", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, prompted("u1", "work"),
				writtenAt(ranCommand("c1", "model", ""), at.Add(time.Minute)),
				writtenAt(ranPrinted("o1", "model", "", "Kept model as `Opus 5.5`"), at.Add(time.Minute)))
		}, Engine{ModelName: "Opus 5.5", ModelAt: at.Add(time.Minute)}},
		{"Claude Code's cancelled /model, recorded as system entries", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, ranOn("a1", "reply-1", "claude-opus-5-5", ""), writtenAt(ranCommand("c1", "model", ""), at.Add(time.Minute)), writtenAt(ranPrinted("o1", "model", "", "Cancelled"), at.Add(time.Minute)))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at}},
		{"Claude Code's /effort with a level, recorded as system entries", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, ranOn("a1", "reply-1", "claude-opus-5-5", "high"), writtenAt(ranCommand("c1", "effort", "max"), at.Add(time.Minute)), writtenAt(ranPrinted("o1", "effort", "max", "Set effort level to max"), at.Add(time.Minute)))
		}, Engine{Model: "claude-opus-5-5", ModelAt: at, Effort: "max", EffortAt: at.Add(time.Minute)}},
		{"Claude Code's command recorded as a user message, with what it printed as a system entry", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, modelCommand("c1", ""), writtenAt(ranPrinted("o1", "model", "", "Set model to `Fable 5.2`"), at.Add(time.Minute)))
		}, Engine{ModelName: "Fable 5.2", ModelAt: at.Add(time.Minute)}},
		{"a system entry that is no command's record sets nothing", "claude", func(t *testing.T, home string) string {
			return conversation(t, home, ranCommand("c1", "model", ""), system("s1", "informational", object{"content": "<local-command-stdout>Set model to `Fable 5.2`</local-command-stdout>", "level": "info"}))
		}, Engine{}},
		{"Codex's turn context", "codex", func(t *testing.T, home string) string {
			first := codexTurnContext("gpt-6-luna")
			first["payload"].(object)["effort"] = "high"
			first["timestamp"] = at.Format(time.RFC3339Nano)
			second := codexTurnContext("gpt-6-astra")
			second["payload"].(object)["effort"] = "xhigh"
			second["timestamp"] = at.Add(time.Minute).Format(time.RFC3339Nano)
			return codexRollout(t, home, "engine", codexMeta("engine", workDir), first, second)
		}, Engine{Model: "gpt-6-astra", ModelAt: at.Add(time.Minute), Effort: "xhigh", EffortAt: at.Add(time.Minute)}},
		{"pi's model and thinking level changes", "pi", func(t *testing.T, home string) string {
			return piSession(t, home, piHeader(),
				piEntry("model_change", "m1", "", object{"provider": "acme", "modelId": "thinker-1"}),
				piEntry("thinking_level_change", "e1", "m1", object{"thinkingLevel": "low"}),
				piMessageEntry("u1", "e1", "user", object{"content": "work"}),
				writtenAt(piEntry("model_change", "m2", "u1", object{"provider": "openrouter", "modelId": "z-ai/glm-5.3-flash"}), at.Add(time.Minute)),
				writtenAt(piEntry("thinking_level_change", "e2", "m2", object{"thinkingLevel": "high"}), at.Add(2*time.Minute)))
		}, Engine{Model: "openrouter/z-ai/glm-5.3-flash", ModelAt: at.Add(time.Minute), Effort: "high", EffortAt: at.Add(2 * time.Minute)}},
		{"pi's answer names the model that wrote it", "pi", func(t *testing.T, home string) string {
			return piSession(t, home, piHeader(),
				piMessageEntry("u1", "", "user", object{"content": "work"}),
				piMessageEntry("a1", "u1", "assistant", object{"provider": "acme", "model": "thinker-1", "content": []object{{"type": "text", "text": "done"}}}))
		}, Engine{Model: "acme/thinker-1", ModelAt: at}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			path := test.path(t, home)
			reader := Reader{Home: home}

			// Act
			got, err := reader.SessionEngine("task-1", test.harness, path)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("engine = %+v, want %+v", got, test.want)
			}
		})
	}
}

// The harness appends to its record, and the next read picks up from where
// the last one ended, so a change typed later is read without the whole
// conversation again.
func TestASessionsEngineFollowsWhatItsHarnessAppends(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home, prompted("u1", "work"), ranOn("a1", "reply-1", "claude-opus-5-5", "high"))
	reader := Reader{Home: home}
	before, err := reader.SessionEngine("task-1", "claude", path)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	appendLines(t, path, writtenAt(ranOn("a2", "reply-2", "claude-fable-5-2", "xhigh"), at.Add(time.Minute)))
	after, err := reader.SessionEngine("task-1", "claude", path)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if want := (Engine{Model: "claude-opus-5-5", ModelAt: at, Effort: "high", EffortAt: at}); before != want {
		t.Fatalf("first read = %+v, want %+v", before, want)
	}
	if want := (Engine{Model: "claude-fable-5-2", ModelAt: at.Add(time.Minute), Effort: "xhigh", EffortAt: at.Add(time.Minute)}); after != want {
		t.Fatalf("read after the harness appended = %+v, want %+v", after, want)
	}
}

// A task that moves to another conversation, as a resume onto a new session
// does, is read from that conversation alone.
func TestASessionsEngineIsReadFromTheConversationItIsIn(t *testing.T) {
	// Arrange
	home := t.TempDir()
	first := conversation(t, home, ranOn("a1", "reply-1", "claude-opus-5-5", "high"))
	second := piSession(t, home, piHeader(), piEntry("model_change", "m1", "", object{"provider": "acme", "modelId": "thinker-1"}))
	reader := Reader{Home: home}
	if _, err := reader.SessionEngine("task-1", "claude", first); err != nil {
		t.Fatal(err)
	}

	// Act
	got, err := reader.SessionEngine("task-1", "pi", second)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if want := (Engine{Model: "acme/thinker-1", ModelAt: at}); got != want {
		t.Fatalf("engine = %+v, want %+v", got, want)
	}
}

// Forget drops the reading of a task that is no longer live, and keeps a live
// task's and the CFO's, whose conversation no task's liveness speaks for, so
// each is read on from where its last read ended.
func TestForgettingEndedTasksKeepsTheCFOsAndLiveTasksReadings(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home, ranOn("a1", "reply-1", "claude-opus-5-5", "high"))
	reader := Reader{Home: home}
	for _, owner := range []string{CFOOwner, "live", "ended"} {
		if _, err := reader.SessionEngine(owner, "claude", path); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	reader.Forget(map[string]bool{"live": true})

	// Assert
	for owner, isKept := range map[string]bool{CFOOwner: true, "live": true, "ended": false} {
		if _, isRead := reader.EngineAgain(owner); isRead != isKept {
			t.Errorf("after Forget, %s has a reading to read on = %v, want %v", owner, isRead, isKept)
		}
	}
}

// A read of a goblin's tree carries the engine its own conversation names,
// whichever harness wrote it: the supervisor keeps the task's record on it,
// so a Codex or pi goblin's record follows its terminal as a Claude Code
// goblin's does.
func TestAGoblinsTreeCarriesTheEngineItsOwnConversationNames(t *testing.T) {
	for name, test := range map[string]struct {
		goblin func(t *testing.T, home string) Goblin
		want   Engine
	}{
		"Codex": {func(t *testing.T, home string) Goblin {
			worktree := t.TempDir()
			turn := codexTurnContext("gpt-6-astra")
			turn["payload"].(object)["effort"] = "xhigh"
			turn["timestamp"] = at.Format(time.RFC3339Nano)
			codexRollout(t, home, "engine", codexMeta("engine", worktree), turn)
			return Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex", Backend: "native", Worktree: worktree}}
		}, Engine{Model: "gpt-6-astra", ModelAt: at, Effort: "xhigh", EffortAt: at}},
		"pi": {func(t *testing.T, home string) Goblin {
			piSession(t, home, piHeader(),
				piEntry("model_change", "m1", "", object{"provider": "openrouter", "modelId": "z-ai/glm-5.3-flash"}),
				piEntry("thinking_level_change", "e1", "m1", object{"thinkingLevel": "high"}))
			return Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "pi"}, Session: "pi-1"}
		}, Engine{Model: "openrouter/z-ai/glm-5.3-flash", ModelAt: at, Effort: "high", EffortAt: at}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			goblin := test.goblin(t, home)
			reader := Reader{Home: home, Now: func() time.Time { return at }}

			// Act
			tree, _ := reader.Read(context.Background(), goblin)

			// Assert
			if tree.Engine != test.want {
				t.Fatalf("the tree's engine = %+v, want %+v", tree.Engine, test.want)
			}
		})
	}
}
