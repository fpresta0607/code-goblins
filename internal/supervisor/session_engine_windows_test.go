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
	// Arrange: a goblin the board has read once, whose terminal is then told
	// another model.
	service, h, userHome, meta := engineGoblin(t, "streamed", "claude-opus-5-5", "high")
	service.readTrees(context.Background())
	sent := service.revision
	said(t, userHome, meta.ID, typedCommand(t, time.Now(), "/model", "claude-fable-5-2", "Set model to `claude-fable-5-2`")...)

	// Act
	service.keepStreamedEngine(meta.ID)

	// Assert
	recorded, err := state.ReadTaskMeta(h.State, meta.ID)
	if err != nil || recorded.Model != "claude-fable-5-2" || recorded.Effort != "high" || service.revision == sent {
		t.Fatalf("task record = %s at %s err=%v, boards sent=%v; want claude-fable-5-2 at high, sent to the boards", recorded.Model, recorded.Effort, err, service.revision != sent)
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
