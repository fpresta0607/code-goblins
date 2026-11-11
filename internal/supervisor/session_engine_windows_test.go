package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// engineEntries counts the entries engineEntry made, which names each.
var engineEntries atomic.Int64

// engineEntry is one entry of a Claude Code conversation, written at when.
func engineEntry(t *testing.T, when time.Time, entry map[string]any) string {
	t.Helper()
	entry["uuid"] = "entry-" + strconv.FormatInt(engineEntries.Add(1), 10)
	entry["timestamp"] = when.UTC().Format(time.RFC3339Nano)
	line, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

// typedCommand is a command typed in a Claude Code terminal and what it
// printed, as Claude Code records the two.
func typedCommand(t *testing.T, when time.Time, command, value, printed string) []string {
	t.Helper()
	return []string{
		engineEntry(t, when, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "<command-name>" + command + "</command-name>\n<command-message>" + strings.TrimPrefix(command, "/") + "</command-message>\n<command-args>" + value + "</command-args>"}}),
		engineEntry(t, when, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "<local-command-stdout>" + printed + "</local-command-stdout>"}}),
	}
}

// answeredOn is an answer of a Claude Code session, which names the model
// and the effort it ran at.
func answeredOn(t *testing.T, when time.Time, model, effort string) string {
	t.Helper()
	return engineEntry(t, when, map[string]any{"type": "assistant", "effort": effort, "message": map[string]any{"id": "reply-" + strconv.FormatInt(engineEntries.Load(), 10), "role": "assistant", "model": model, "content": []map[string]any{{"type": "text", "text": "Done."}}}})
}

// said appends entries to the conversation treeGoblin gave goblin id.
func said(t *testing.T, userHome, id string, entries ...string) {
	t.Helper()
	written(t, filepath.Join(userHome, ".claude", "projects", "C--work-"+id, id+"-conversation.jsonl"), entries...)
}

// engineGoblin is a running Claude Code goblin whose record names model and
// effort, with the board that reads it.
func engineGoblin(t *testing.T, id, model, effort string) (*Service, home.Home, string, state.TaskMeta) {
	t.Helper()
	store, h := testStore(t)
	userHome := t.TempDir()
	meta := treeGoblin(t, h.State, userHome, id, true)
	meta.Model, meta.Effort = model, effort
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	return &Service{Store: store, Instance: "engine", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}, h, userHome, meta
}

// shownEngine is the model and effort the board's snapshot gives task id,
// which its card's tooltip, Modern Chat's box, the Classic panel and its
// details all read.
func shownEngine(t *testing.T, service *Service, id string) (string, string) {
	t.Helper()
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range snapshot.Tasks {
		if task.ID == id {
			return task.Model, task.Effort
		}
	}
	t.Fatalf("the snapshot holds no task %s", id)
	return "", ""
}

// A model and an effort changed by hand in a goblin's terminal are read back
// from its harness's own session record into the task's one record, which
// every view of the session reads, and the boards are sent it at once (the
// Overlord, 2026-11-10, of a card that read Opus 5.5 beside a terminal on
// Fable 5.2: "The board tooltips are not in sync with the model selected").
func TestAnEngineChangedInAGoblinsTerminalBecomesItsTasksRecord(t *testing.T) {
	now := time.Now()
	for name, testCase := range map[string]struct {
		entries    []string
		catalog    string
		wantModel  string
		wantEffort string
	}{
		"a model typed with its id, before any answer": {
			entries:   typedCommand(t, now, "/model", "claude-fable-5-2", "Set model to `claude-fable-5-2`"),
			wantModel: "claude-fable-5-2", wantEffort: "high",
		},
		"a model picked by its name, which the catalog gives an id": {
			entries:   typedCommand(t, now, "/model", "", "Kept model as `Fable 5.2`"),
			catalog:   `{"additionalModelOptionsCache":[{"value":"claude-fable-5-2","label":"Fable 5.2"}]}`,
			wantModel: "claude-fable-5-2", wantEffort: "high",
		},
		"a model picked by a name no catalog knows, until an answer names it": {
			entries:   typedCommand(t, now, "/model", "", "Set model to `Fable 5.2`"),
			wantModel: "claude-opus-5-5", wantEffort: "high",
		},
		"the model and effort an answer ran at": {
			entries:   []string{answeredOn(t, now, "claude-fable-5-2", "max")},
			wantModel: "claude-fable-5-2", wantEffort: "max",
		},
		"an effort typed": {
			entries:   typedCommand(t, now, "/effort", "xhigh", "Set effort level to xhigh (this session only): Deepest reasoning"),
			wantModel: "claude-opus-5-5", wantEffort: "xhigh",
		},
		"an effort left to the harness, which no start can name": {
			entries:   typedCommand(t, now, "/effort", "auto", "Effort level set to auto"),
			wantModel: "claude-opus-5-5", wantEffort: "default",
		},
		"an answer on the recorded model at the recorded effort": {
			entries:   []string{answeredOn(t, now, "claude-opus-5-5", "high")},
			wantModel: "claude-opus-5-5", wantEffort: "high",
		},
		"the harness's own pick asked for, which no start can name": {
			entries:   typedCommand(t, now, "/model", "default", "Set model to `Default (recommended)`"),
			wantModel: "claude-opus-5-5", wantEffort: "high",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			service, h, userHome, meta := engineGoblin(t, "terminal", "claude-opus-5-5", "high")
			if testCase.catalog != "" {
				writeFile(t, filepath.Join(userHome, ".claude.json"), testCase.catalog)
			}
			service.readTrees(context.Background())
			sent := service.revision
			said(t, userHome, meta.ID, testCase.entries...)

			// Act
			service.readTrees(context.Background())

			// Assert
			recorded, err := state.ReadTaskMeta(h.State, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			if recorded.Model != testCase.wantModel || recorded.Effort != testCase.wantEffort {
				t.Fatalf("task record = %s at %s, want %s at %s", recorded.Model, recorded.Effort, testCase.wantModel, testCase.wantEffort)
			}
			if model, effort := shownEngine(t, service, meta.ID); model != testCase.wantModel || effort != testCase.wantEffort {
				t.Fatalf("snapshot = %s at %s, want %s at %s", model, effort, testCase.wantModel, testCase.wantEffort)
			}
			isChanged := testCase.wantModel != meta.Model || testCase.wantEffort != meta.Effort
			if isSent := service.revision != sent; isSent != isChanged {
				t.Fatalf("boards sent a fresh snapshot = %v, want %v", isSent, isChanged)
			}
			recorded.Model, recorded.Effort = meta.Model, meta.Effort
			if !reflect.DeepEqual(recorded, meta) {
				t.Fatalf("task record = %+v, want only its model and effort changed from %+v", recorded, meta)
			}
		})
	}
}

// A record that names a model with its context window keeps it while the
// harness answers on that model, which a transcript names without one.
func TestARecordedContextWindowOutlivesAnAnswerOnItsModel(t *testing.T) {
	// Arrange
	service, h, userHome, meta := engineGoblin(t, "window", "claude-opus-5-5[1m]", "high")
	said(t, userHome, meta.ID, answeredOn(t, time.Now(), "claude-opus-5-5", "high"))

	// Act
	service.readTrees(context.Background())

	// Assert
	recorded, err := state.ReadTaskMeta(h.State, meta.ID)
	if err != nil || !reflect.DeepEqual(recorded, meta) {
		t.Fatalf("task record = %+v err=%v, want it unchanged from %+v", recorded, err, meta)
	}
}

// A record names a Claude Code model as a start takes it: by its id, or by an
// alias for the newest model of a family, either with the context window
// asked for after it. Claude Code's answers name the id alone, so the record
// keeps the alias and the window while the answers name that model, and takes
// another model's id once an answer names one. What is typed after /model is
// itself what a start takes, so the record takes it as typed, whatever it
// named before, and keeps it through the answers on that model. Each holds
// however the reads fall. The models are the routing lanes' own aliases and
// what Claude Code's /model takes: sonnet names claude-sonnet-5-5, opus[1m]
// claude-opus-5-5 on its 1M window, and claude-opus-5 and claude-sonnet-5 are
// other models than claude-opus-5-5 and claude-sonnet-5-5.
func TestARecordedAliasOutlivesAnswersOnTheModelItNames(t *testing.T) {
	now := time.Now()
	for name, testCase := range map[string]struct {
		recorded string
		entries  []string
		want     string
	}{
		"an alias and its window typed, then an answer on its model": {
			recorded: "claude-opus-5-5",
			entries:  append(typedCommand(t, now, "/model", "opus[1m]", "Set model to `Opus 5.5 (1M context)`"), answeredOn(t, now, "claude-opus-5-5", "high")),
			want:     "opus[1m]",
		},
		"an id and its window typed over an alias of that model": {
			recorded: "sonnet",
			entries:  append(typedCommand(t, now, "/model", "claude-sonnet-5-5[1m]", "Set model to `Sonnet 5.5 (1M context)`"), answeredOn(t, now, "claude-sonnet-5-5", "high")),
			want:     "claude-sonnet-5-5[1m]",
		},
		"an older id typed over an alias of its family": {
			recorded: "sonnet",
			entries:  append(typedCommand(t, now, "/model", "claude-sonnet-5", "Set model to `Sonnet 5`"), answeredOn(t, now, "claude-sonnet-5", "high")),
			want:     "claude-sonnet-5",
		},
		"an alias typed without the window its record asked for": {
			recorded: "opus[1m]",
			entries:  append(typedCommand(t, now, "/model", "opus", "Set model to `Opus 5.5`"), answeredOn(t, now, "claude-opus-5-5", "high")),
			want:     "opus",
		},
		"an id typed without the window its record asked for": {
			recorded: "claude-opus-5-5[1m]",
			entries:  append(typedCommand(t, now, "/model", "claude-opus-5-5", "Set model to `Opus 5.5`"), answeredOn(t, now, "claude-opus-5-5", "high")),
			want:     "claude-opus-5-5",
		},
		"a spawned alias while the answers name its model": {
			recorded: "sonnet",
			entries:  []string{answeredOn(t, now, "claude-sonnet-5-5", "high"), answeredOn(t, now, "claude-sonnet-5-5", "high")},
			want:     "sonnet",
		},
		"a spawned alias once an answer names another model": {
			recorded: "sonnet",
			entries:  []string{answeredOn(t, now, "claude-sonnet-5-5", "high"), answeredOn(t, now, "claude-opus-5-5", "high")},
			want:     "claude-opus-5-5",
		},
		"an id, which is no alias of a newer model's": {
			recorded: "claude-opus-5",
			entries:  []string{answeredOn(t, now, "claude-opus-5-5", "high")},
			want:     "claude-opus-5-5",
		},
	} {
		apart := [][]string{}
		for _, entry := range testCase.entries {
			apart = append(apart, []string{entry})
		}
		for fall, reads := range map[string][][]string{"read together": {testCase.entries}, "read as each is written": apart} {
			t.Run(name+", "+fall, func(t *testing.T) {
				// Arrange
				service, h, userHome, meta := engineGoblin(t, "alias", testCase.recorded, "high")

				// Act
				for _, entries := range reads {
					said(t, userHome, meta.ID, entries...)
					service.readTrees(context.Background())
				}

				// Assert
				recorded, err := state.ReadTaskMeta(h.State, meta.ID)
				if err != nil || recorded.Model != testCase.want || recorded.Effort != "high" {
					t.Fatalf("task record = %s at %s err=%v, want %s at high", recorded.Model, recorded.Effort, err, testCase.want)
				}
				if model, _ := shownEngine(t, service, meta.ID); model != testCase.want {
					t.Fatalf("snapshot = %s, want %s", model, testCase.want)
				}
			})
		}
	}
}

// A conversation resumed across a start keeps what its earlier run named,
// and the session that started is the one the record follows: an answer
// written since the start names the model that runs, whether the earlier run
// answered on that model or was told it after /model, and what the earlier
// run was told never replaces the record the start was given.
func TestAnAnswerSinceAStartNamesTheModelThatRunsWhateverWasNamedBefore(t *testing.T) {
	for name, testCase := range map[string]struct {
		recorded string
		earlier  func(t *testing.T, when time.Time) []string
		want     string
	}{
		"an earlier run that answered on the model": {
			recorded: "claude-fable-5-2",
			earlier: func(t *testing.T, when time.Time) []string {
				return []string{answeredOn(t, when, "claude-opus-5-5", "max")}
			},
			want: "claude-opus-5-5",
		},
		"an earlier run that was told the model by an alias and its window": {
			recorded: "claude-fable-5-2",
			earlier: func(t *testing.T, when time.Time) []string {
				return typedCommand(t, when, "/model", "opus[1m]", "Set model to `Opus 5.5 (1M context)`")
			},
			want: "claude-opus-5-5",
		},
		"an earlier run that was told a window the start was not given": {
			recorded: "claude-opus-5-5",
			earlier: func(t *testing.T, when time.Time) []string {
				return typedCommand(t, when, "/model", "opus[1m]", "Set model to `Opus 5.5 (1M context)`")
			},
			want: "claude-opus-5-5",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			service, h, userHome, meta := engineGoblin(t, "resumed", testCase.recorded, "max")
			said(t, userHome, meta.ID, append(testCase.earlier(t, spawnTime(meta.SpawnGen).Add(-time.Minute)), answeredOn(t, time.Now(), "claude-opus-5-5", "max"))...)

			// Act
			service.readTrees(context.Background())

			// Assert
			recorded, err := state.ReadTaskMeta(h.State, meta.ID)
			if err != nil || recorded.Model != testCase.want || recorded.Effort != "max" {
				t.Fatalf("task record = %s at %s err=%v, want %s at max", recorded.Model, recorded.Effort, err, testCase.want)
			}
			if model, _ := shownEngine(t, service, meta.ID); model != testCase.want {
				t.Fatalf("snapshot = %s, want %s", model, testCase.want)
			}
		})
	}
}

// A switch and a resume write the record the relaunch starts on, and what
// the conversation held before that start, which the same conversation keeps,
// never puts the earlier engine back.
func TestWhatAConversationNamedBeforeItsStartNeverReplacesTheRecord(t *testing.T) {
	// Arrange: a goblin switched to Fable an hour ago, whose resumed
	// conversation still holds the answers its earlier run gave on Opus.
	service, h, userHome, meta := engineGoblin(t, "switched", "claude-fable-5-2", "max")
	earlier := spawnTime(meta.SpawnGen).Add(-time.Minute)
	said(t, userHome, meta.ID, append(typedCommand(t, earlier, "/model", "claude-opus-5-5", "Set model to `claude-opus-5-5`"), answeredOn(t, earlier, "claude-opus-5-5", "low"))...)

	// Act
	service.readTrees(context.Background())

	// Assert
	recorded, err := state.ReadTaskMeta(h.State, meta.ID)
	if err != nil || !reflect.DeepEqual(recorded, meta) {
		t.Fatalf("task record = %+v err=%v, want it unchanged from %+v", recorded, err, meta)
	}
}

// A record another command is writing is left to it: the next read writes
// the engine, so no reading is lost and no writer is overwritten.
func TestAnEngineReadWhileTheRecordIsHeldWaitsForTheNextRead(t *testing.T) {
	// Arrange
	service, h, userHome, meta := engineGoblin(t, "held", "claude-opus-5-5", "high")
	said(t, userHome, meta.ID, answeredOn(t, time.Now(), "claude-fable-5-2", "high"))
	release := holdMetadata(t, h.State, meta.ID)

	// Act
	service.readTrees(context.Background())
	held, err := state.ReadTaskMeta(h.State, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	release()
	service.readTrees(context.Background())

	// Assert
	recorded, err := state.ReadTaskMeta(h.State, meta.ID)
	if err != nil || held.Model != "claude-opus-5-5" || recorded.Model != "claude-fable-5-2" {
		t.Fatalf("model while held = %s, after = %s err=%v, want claude-opus-5-5 then claude-fable-5-2", held.Model, recorded.Model, err)
	}
}

// A switch gives the record a new generation, and may give it another
// harness, between the read of a goblin's tree and the write of what that
// tree named. The engine read for the session that ended is not the new
// one's, so the record the switch wrote stays as it wrote it.
func TestAnEngineReadForASessionThatEndedLeavesTheSwitchedRecordAlone(t *testing.T) {
	for name, switched := range map[string]func(meta *state.TaskMeta){
		"another generation": func(meta *state.TaskMeta) { meta.SpawnGen = "s" + strconv.FormatInt(time.Now().UnixNano(), 10) },
		"another harness":    func(meta *state.TaskMeta) { meta.Harness = "codex" },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange: the tree read saw the goblin answer on Sonnet, and the
			// switch to Fable wrote the record before that reading is kept.
			service, h, _, read := engineGoblin(t, "raced", "claude-opus-5-5", "high")
			want := read
			want.Model = "claude-fable-5-2"
			switched(&want)
			if err := state.WriteTaskMeta(h.State, want); err != nil {
				t.Fatal(err)
			}
			answered := time.Now()

			// Act
			isChanged, err := service.keepEngine(read, fleettree.Engine{Model: "claude-sonnet-5-5", ModelAt: answered, Effort: "low", EffortAt: answered})

			// Assert
			recorded, readErr := state.ReadTaskMeta(h.State, read.ID)
			if err != nil || readErr != nil || isChanged || !reflect.DeepEqual(recorded, want) {
				t.Fatalf("task record = %+v changed=%v err=%v %v, want it left as the switch wrote it: %+v", recorded, isChanged, err, readErr, want)
			}
		})
	}
}

// holdMetadata takes the lock on task id's record, as a command that changes
// the record does, and answers what lets it go.
func holdMetadata(t *testing.T, stateDir, id string) func() {
	t.Helper()
	if _, err := lock.AcquireExclusiveNamed(stateDir, state.MetadataLockName(id)); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := lock.ReleaseExclusiveNamed(stateDir, state.MetadataLockName(id)); err != nil {
			t.Fatal(err)
		}
	}
}

// engineCFO is a Claude Code CFO registered in native terminal cfo, whose
// terminal started an hour ago, with the board that reads its conversation
// and where Claude Code keeps that conversation.
func engineCFO(t *testing.T) (*Service, home.Home, string) {
	t.Helper()
	store, h := testStore(t)
	userHome := t.TempDir()
	primary := primaryRegistration{Host: NativeCFOTerminal, Agent: "claude", Process: thisProcess(t)}
	primary.Process.Session = "cfo-conversation"
	registration, err := json.Marshal(primary)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "primary.json"), string(registration))
	record, err := json.Marshal(host.Record{ID: NativeCFOTerminal, Pipe: `\\.\pipe\code-goblins-host-engine-cfo`, Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: primary.Process.PID, ChildStart: primary.Process.Start, Started: time.Now().Add(-time.Hour).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "hosts", NativeCFOTerminal+".json"), string(record))
	conversation := filepath.Join(cfoConversationFile(userHome, h.Root), "cfo-conversation.jsonl")
	writeFile(t, conversation, "")
	return &Service{Store: store, Instance: "engine", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}, h, conversation
}

// written appends entries to the conversation at path.
func written(t *testing.T, path string, entries ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(entries, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

// The CFO's one record of its model and effort is its engine file, which a
// change made by hand in its terminal reaches as a goblin's reaches its task
// record: the file follows what the CFO's harness says it runs, and its next
// start uses it. A selection that waits to be applied, or one a problem holds
// for the CFO's next start, is the Overlord's own choice and is left as saved.
func TestAnEngineChangedInTheCFOsTerminalBecomesItsEngineRecord(t *testing.T) {
	now := time.Now()
	for name, testCase := range map[string]struct {
		saved   string
		entries []string
		want    CFOEngine
		isKept  bool
	}{
		"a CFO with no engine saved": {
			entries: []string{answeredOn(t, now, "claude-opus-5-5", "high")},
			want:    CFOEngine{Harness: "claude", Model: "claude-opus-5-5", Effort: "high"},
		},
		"a CFO whose answers name no effort": {
			entries: []string{answeredOn(t, now, "claude-opus-5-5", "")},
			want:    CFOEngine{Harness: "claude", Model: "claude-opus-5-5", Effort: "default"},
		},
		"a model typed over an applied selection": {
			saved:   `{"harness":"claude","model":"claude-opus-5-5","effort":"high"}`,
			entries: typedCommand(t, now, "/model", "claude-fable-5-2", "Set model to `claude-fable-5-2`"),
			want:    CFOEngine{Harness: "claude", Model: "claude-fable-5-2", Effort: "high"},
		},
		"an effort typed over an applied selection": {
			saved:   `{"harness":"claude","model":"claude-opus-5-5","effort":"high"}`,
			entries: typedCommand(t, now, "/effort", "max", "Set effort level to max"),
			want:    CFOEngine{Harness: "claude", Model: "claude-opus-5-5", Effort: "max"},
		},
		"a CFO started as another harness than its selection, once it names its engine": {
			saved:   `{"harness":"codex","model":"gpt-6-astra","effort":"high","problem":"The CFO started as claude","overridden":"claude"}`,
			entries: []string{answeredOn(t, now, "claude-fable-5-2", "xhigh")},
			want:    CFOEngine{Harness: "claude", Model: "claude-fable-5-2", Effort: "xhigh"},
		},
		"a selection that waits to be applied": {
			saved:   `{"harness":"claude","model":"claude-fable-5-2","effort":"max","pending":true}`,
			entries: []string{answeredOn(t, now, "claude-opus-5-5", "high")},
			isKept:  true,
		},
		"a selection a problem holds for the next start": {
			saved:   `{"harness":"claude","model":"claude-fable-5-2","effort":"max","problem":"The selection could not be applied"}`,
			entries: []string{answeredOn(t, now, "claude-opus-5-5", "high")},
			isKept:  true,
		},
		"what the conversation named before the terminal started": {
			saved:   `{"harness":"claude","model":"claude-fable-5-2","effort":"max"}`,
			entries: []string{answeredOn(t, now.Add(-2*time.Hour), "claude-opus-5-5", "high")},
			isKept:  true,
		},
		"the harness's own pick asked for, which no start can name": {
			saved:   `{"harness":"claude","model":"claude-opus-5-5","effort":"high"}`,
			entries: typedCommand(t, now, "/model", "default", "Set model to `Default (recommended)`"),
			isKept:  true,
		},
		"an alias and its window typed, with the answer on its model": {
			saved:   `{"harness":"claude","model":"claude-opus-5-5","effort":"high"}`,
			entries: append(typedCommand(t, now, "/model", "opus[1m]", "Set model to `Opus 5.5 (1M context)`"), answeredOn(t, now, "claude-opus-5-5", "high")),
			want:    CFOEngine{Harness: "claude", Model: "opus[1m]", Effort: "high"},
		},
		"an id and its window typed over a saved alias of that model": {
			saved:   `{"harness":"claude","model":"sonnet","effort":"high"}`,
			entries: append(typedCommand(t, now, "/model", "claude-sonnet-5-5[1m]", "Set model to `Sonnet 5.5 (1M context)`"), answeredOn(t, now, "claude-sonnet-5-5", "high")),
			want:    CFOEngine{Harness: "claude", Model: "claude-sonnet-5-5[1m]", Effort: "high"},
		},
		"a CFO with no engine saved, restarted on a conversation that answered on its model before": {
			entries: []string{answeredOn(t, now.Add(-2*time.Hour), "claude-opus-5-5", "low"), answeredOn(t, now, "claude-opus-5-5", "high")},
			want:    CFOEngine{Harness: "claude", Model: "claude-opus-5-5", Effort: "high"},
		},
		"a CFO with no engine saved, restarted on a conversation that was told its model before": {
			entries: append(typedCommand(t, now.Add(-2*time.Hour), "/model", "opus[1m]", "Set model to `Opus 5.5 (1M context)`"), answeredOn(t, now, "claude-opus-5-5", "high")),
			want:    CFOEngine{Harness: "claude", Model: "claude-opus-5-5", Effort: "high"},
		},
		"a saved alias while the answers name its model": {
			saved:   `{"harness":"claude","model":"sonnet","effort":"high"}`,
			entries: []string{answeredOn(t, now, "claude-sonnet-5-5", "high")},
			isKept:  true,
		},
		"a saved alias once an answer names another model": {
			saved:   `{"harness":"claude","model":"sonnet","effort":"high"}`,
			entries: []string{answeredOn(t, now, "claude-sonnet-5-5", "high"), answeredOn(t, now, "claude-opus-5-5", "high")},
			want:    CFOEngine{Harness: "claude", Model: "claude-opus-5-5", Effort: "high"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			service, h, conversation := engineCFO(t)
			path := filepath.Join(h.State, "cfo-engine.json")
			if testCase.saved != "" {
				writeFile(t, path, testCase.saved)
			}
			written(t, conversation, testCase.entries...)
			sent := service.revision

			// Act
			service.readTrees(context.Background())

			// Assert
			if testCase.isKept {
				kept, err := os.ReadFile(path)
				if err != nil || string(kept) != testCase.saved || service.revision != sent {
					t.Fatalf("engine record = %s err=%v, want it left as saved: %s", kept, err, testCase.saved)
				}
				if _, err := ReadCFOEngine(h.State); err != nil {
					t.Fatalf("the engine record names nothing the fleet can start: %v", err)
				}
				return
			}
			recorded, err := ReadCFOEngine(h.State)
			if err != nil || recorded != testCase.want {
				t.Fatalf("engine record = %+v err=%v, want %+v", recorded, err, testCase.want)
			}
			if service.revision == sent {
				t.Fatal("the boards were sent no fresh snapshot of the CFO's engine")
			}
		})
	}
}

// A chat view that is open reads the session's conversation as it is
// written, and the record follows on that read rather than waiting for the
// board's next read of every tree: a model typed in a goblin's terminal shows
// on its card within the view's own tick.
func TestAnOpenChatViewKeepsAGoblinsRecordAsItsConversationSpeaks(t *testing.T) {
	// Arrange: a goblin the board has read once, whose chat view streams the
	// task's own messages.
	service, h, userHome, meta := engineGoblin(t, "streamed", "claude-opus-5-5", "high")
	service.Instance = "board-token"
	service.readTrees(context.Background())
	sent := service.Revision()
	next := messageStream(t, service, "task="+meta.ID+"&generation="+meta.SpawnGen)
	next("page")

	// Act: its terminal is told another model, and no tree is read again.
	said(t, userHome, meta.ID, typedCommand(t, time.Now(), "/model", "claude-fable-5-2", "Set model to `claude-fable-5-2`")...)
	next("messages")

	// Assert: the record follows the read that sent those messages, and the
	// boards are sent the snapshot that names it.
	var recorded state.TaskMeta
	var err error
	for range 50 {
		if recorded, err = state.ReadTaskMeta(h.State, meta.ID); err == nil && recorded.Model == "claude-fable-5-2" && service.Revision() != sent {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil || recorded.Model != "claude-fable-5-2" || recorded.Effort != "high" || service.Revision() == sent {
		t.Fatalf("task record = %s at %s err=%v, boards sent=%v; want claude-fable-5-2 at high, sent to the boards", recorded.Model, recorded.Effort, err, service.Revision() != sent)
	}
	if model, effort := shownEngine(t, service, meta.ID); model != "claude-fable-5-2" || effort != "high" {
		t.Fatalf("snapshot = %s at %s, want claude-fable-5-2 at high", model, effort)
	}
}

// The CFO's record keeps an alias and its window typed in its terminal as a
// goblin's does, when the answer on that model is read after the command.
func TestTheCFOsRecordedAliasOutlivesALaterReadOfAnAnswerOnItsModel(t *testing.T) {
	// Arrange
	service, h, conversation := engineCFO(t)
	writeFile(t, filepath.Join(h.State, "cfo-engine.json"), `{"harness":"claude","model":"claude-opus-5-5","effort":"high"}`)
	written(t, conversation, typedCommand(t, time.Now(), "/model", "opus[1m]", "Set model to `Opus 5.5 (1M context)`")...)
	service.readTrees(context.Background())

	// Act
	written(t, conversation, answeredOn(t, time.Now(), "claude-opus-5-5", "high"))
	service.readTrees(context.Background())

	// Assert
	want := CFOEngine{Harness: "claude", Model: "opus[1m]", Effort: "high"}
	if recorded, err := ReadCFOEngine(h.State); err != nil || recorded != want {
		t.Fatalf("engine record = %+v err=%v, want %+v", recorded, err, want)
	}
}

// The CFO's open chat view does the same for its engine record, on the
// stream the board reads its messages from.
func TestTheCFOsOpenChatViewKeepsItsEngineRecordAsItsConversationSpeaks(t *testing.T) {
	// Arrange
	service, h, conversation := engineCFO(t)
	service.Instance = "board-token"
	written(t, conversation, answeredOn(t, time.Now(), "claude-opus-5-5", "high"))
	next := cfoMessageStream(t, service)
	next("page")

	// Act
	written(t, conversation, answeredOn(t, time.Now(), "claude-fable-5-2", "max"))
	next("messages")

	// Assert: the record follows the read that sent those messages.
	want := CFOEngine{Harness: "claude", Model: "claude-fable-5-2", Effort: "max"}
	var recorded CFOEngine
	var err error
	for range 50 {
		if recorded, err = ReadCFOEngine(h.State); err == nil && recorded == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("engine record = %+v err=%v, want %+v", recorded, err, want)
}
