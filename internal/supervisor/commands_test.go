package supervisor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/harnessmap"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// writeCommandFixture writes one file, making its folders.
func writeCommandFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// commandBoard is a board whose user's home is a fresh folder, with task-1 of
// the harness harness running in a worktree of its own and no CFO.
func commandBoard(t *testing.T, harness string) (*HTTP, string, string) {
	t.Helper()
	store, _ := testStore(t)
	profile := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCommandFixture(t, filepath.Join(worktree, ".git"), "")
	// Pi persists resolved paths, including Windows temporary-folder aliases.
	worktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(store.Home.State, state.TaskMeta{ID: "task-1", Project: worktree, Worktree: worktree, Harness: harness, SpawnGen: "g1"}); err != nil {
		t.Fatal(err)
	}
	// A harness may keep its configuration elsewhere; these tests read the
	// defaults under the user's home.
	for _, variable := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(variable, "")
	}
	board := NewHTTP(&Service{Store: store, Instance: "test-instance", Options: Options{Tree: &fleettree.Reader{Home: profile}}}, credentialBoardHost, nil)
	if harness == "pi" {
		writeCommandFixture(t, filepath.Join(profile, ".pi", "agent", "settings.json"), `{"defaultProjectTrust":"always"}`)
	}
	return board, profile, worktree
}

// getCommands asks the board for the commands a query names.
func getCommands(board *HTTP, query string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("GET", "http://"+board.Host+"/api/commands?"+query, nil)
	response := httptest.NewRecorder()
	board.ServeHTTP(response, request)
	return response
}

// entriesIn reads a successful answer's commands.
func entriesIn(t *testing.T, response *httptest.ResponseRecorder) []command {
	t.Helper()
	var answer struct{ Commands []command }
	if response.Code != http.StatusOK {
		t.Fatalf("answered %d %s", response.Code, response.Body)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
		t.Fatalf("answered %s (%v)", response.Body, err)
	}
	return answer.Commands
}

// commandsIn reads the names of a successful answer's own commands and
// skills, leaving out the harness's built-ins.
func commandsIn(t *testing.T, response *httptest.ResponseRecorder) []string {
	t.Helper()
	return ownNames(entriesIn(t, response))
}

// ownNames names the entries that are not built-ins, in their order.
func ownNames(entries []command) []string {
	names := []string{}
	for _, entry := range entries {
		if entry.Source != "builtin" {
			names = append(names, entry.Name)
		}
	}
	return names
}

// Each harness offers the commands its own files define: what one harness
// keeps is never offered for another.
func TestCommandsComeFromTheSessionsOwnHarness(t *testing.T) {
	for _, c := range []struct {
		harness string
		want    []string
	}{
		// Claude Code reads .claude/commands and .claude/skills in the folder
		// it runs in, and its own folder under the user's home, never pi's.
		{"claude", []string{"compact", "deploy", "frontend:component", "review"}},
		// pi reads prompt templates and skills from its project, its agent
		// folder and the shared skills folder, and offers a skill as a command.
		{"pi", []string{"plan", "skill:deploy", "skill:pdf", "skill:review"}},
		// Codex defines no command files, so it offers only its built-ins.
		{"codex", []string{}},
	} {
		t.Run(c.harness, func(t *testing.T) {
			// Arrange
			board, profile, worktree := commandBoard(t, c.harness)
			writeCommandFixture(t, filepath.Join(worktree, ".claude", "commands", "compact.md"), "x")
			writeCommandFixture(t, filepath.Join(worktree, ".claude", "commands", "frontend", "component.md"), "x")
			writeCommandFixture(t, filepath.Join(worktree, ".claude", "skills", "deploy", "SKILL.md"), "x")
			writeCommandFixture(t, filepath.Join(worktree, ".pi", "prompts", "plan.md"), "x")
			writeCommandFixture(t, filepath.Join(worktree, ".pi", "skills", "review", "SKILL.md"), "---\ndescription: Review\n---\nx")
			writeCommandFixture(t, filepath.Join(worktree, ".agents", "skills", "pdf", "SKILL.md"), "---\ndescription: PDF\n---\nx")
			writeCommandFixture(t, filepath.Join(profile, ".claude", "commands", "review.md"), "x")
			writeCommandFixture(t, filepath.Join(profile, ".claude", "skills", "compact", "SKILL.md"), "x")
			writeCommandFixture(t, filepath.Join(profile, ".pi", "agent", "skills", "deploy", "SKILL.md"), "---\ndescription: Deploy\n---\nx")

			// Act
			answered := commandsIn(t, getCommands(board, "task=task-1&token=test-instance"))

			// Assert
			if !reflect.DeepEqual(answered, c.want) {
				t.Fatalf("commands = %v, want %v", answered, c.want)
			}
		})
	}
}

// A harness's own configuration folder is read where the environment says it
// keeps it, and the default is not also read.
func TestACommandFolderTheEnvironmentNamesIsRead(t *testing.T) {
	// Arrange
	board, profile, _ := commandBoard(t, "claude")
	elsewhere := filepath.Join(profile, "elsewhere")
	writeCommandFixture(t, filepath.Join(elsewhere, "commands", "compact.md"), "x")
	writeCommandFixture(t, filepath.Join(profile, ".claude", "commands", "review.md"), "x")
	t.Setenv("CLAUDE_CONFIG_DIR", elsewhere)

	// Act
	answered := commandsIn(t, getCommands(board, "task=task-1&token=test-instance"))

	// Assert
	if want := []string{"compact"}; !reflect.DeepEqual(answered, want) {
		t.Fatalf("commands = %v, want %v", answered, want)
	}
}

// pi's own setting that turns skill commands off leaves the prompt templates,
// and the project's own settings win over the user's.
func TestPiSkillCommandsFollowTheSetting(t *testing.T) {
	for _, c := range []struct {
		what           string
		userSetting    string
		projectSetting string
		want           []string
	}{
		{"unset", "", "", []string{"plan", "skill:tdd"}},
		{"the user turns them off", `{"enableSkillCommands":false}`, "", []string{"plan"}},
		{"the project turns them off", "", `{"enableSkillCommands":false}`, []string{"plan"}},
		{"the project turns them back on", `{"enableSkillCommands":false}`, `{"enableSkillCommands":true}`, []string{"plan", "skill:tdd"}},
	} {
		t.Run(c.what, func(t *testing.T) {
			// Arrange
			board, profile, worktree := commandBoard(t, "pi")
			writeCommandFixture(t, filepath.Join(worktree, ".pi", "prompts", "plan.md"), "x")
			writeCommandFixture(t, filepath.Join(worktree, ".pi", "skills", "tdd", "SKILL.md"), "---\ndescription: TDD\n---\nx")
			agentDir := filepath.Join(profile, ".pi", "agent")
			trust, err := json.Marshal(map[string]bool{worktree: true})
			if err != nil {
				t.Fatal(err)
			}
			writeCommandFixture(t, filepath.Join(agentDir, "trust.json"), string(trust))
			if c.userSetting != "" {
				writeCommandFixture(t, filepath.Join(agentDir, "settings.json"), c.userSetting)
			}
			if c.projectSetting != "" {
				writeCommandFixture(t, filepath.Join(worktree, ".pi", "settings.json"), c.projectSetting)
			}

			// Act
			answered := commandsIn(t, getCommands(board, "task=task-1&token=test-instance"))

			// Assert
			if !reflect.DeepEqual(answered, c.want) {
				t.Fatalf("commands = %v, want %v", answered, c.want)
			}
		})
	}
}

func TestPiCommandsRespectConfiguredResourcesAndTrust(t *testing.T) {
	for _, isTrusted := range []bool{false, true} {
		t.Run(fmt.Sprint(isTrusted), func(t *testing.T) {
			board, profile, worktree := commandBoard(t, "pi")
			agentDir := filepath.Join(profile, ".pi", "agent")
			writeCommandFixture(t, filepath.Join(agentDir, "settings.json"), `{"prompts":["-prompts/hidden.md","extra"],"skills":["custom.md"]}`)
			trust, err := json.Marshal(map[string]bool{worktree: isTrusted})
			if err != nil {
				t.Fatal(err)
			}
			writeCommandFixture(t, filepath.Join(agentDir, "trust.json"), string(trust))
			writeCommandFixture(t, filepath.Join(agentDir, "prompts", "hidden.md"), "hidden")
			writeCommandFixture(t, filepath.Join(agentDir, "extra", "global.md"), "extra")
			writeCommandFixture(t, filepath.Join(agentDir, "custom.md"), "---\nname: custom\ndescription: Custom\n---\nbody")
			writeCommandFixture(t, filepath.Join(worktree, ".pi", "prompts", "project.md"), "project")
			answered := commandsIn(t, getCommands(board, "task=task-1&token=test-instance"))
			want := []string{"global", "skill:custom"}
			if isTrusted {
				want = []string{"global", "project", "skill:custom"}
			}
			if !reflect.DeepEqual(answered, want) {
				t.Fatalf("commands = %v, want %v", answered, want)
			}
		})
	}
}

// A request that names no session the board reads is refused, and one that
// names a session a later start replaced is refused as stale. A path in the
// query names no session, so no folder outside one is ever read.
func TestCommandsRefuseASessionTheBoardCannotName(t *testing.T) {
	board, _, _ := commandBoard(t, "claude")
	for _, c := range []struct {
		what, query string
		status      int
	}{
		{"no token", "task=task-1", http.StatusForbidden},
		{"a token that is not this board's", "task=task-1&token=other", http.StatusForbidden},
		{"no session", "token=test-instance", http.StatusBadRequest},
		{"a task id that is not one", "task=../state&token=test-instance", http.StatusBadRequest},
		{"a folder instead of a session", "task=" + filepath.ToSlash(t.TempDir()) + "&token=test-instance", http.StatusBadRequest},
		{"a task this home does not hold", "task=task-9&token=test-instance", http.StatusNotFound},
		{"a generation a later start replaced", "task=task-1&generation=old&token=test-instance", http.StatusConflict},
		{"a CFO that is not running", "cfo=1&token=test-instance", http.StatusNotFound},
	} {
		t.Run(c.what, func(t *testing.T) {
			// Act
			response := getCommands(board, c.query)

			// Assert
			if response.Code != c.status {
				t.Fatalf("answered %d %s, want %d", response.Code, response.Body, c.status)
			}
		})
	}
}

// A task's own generation is accepted, and the CFO's panel reads the commands
// of the harness the CFO runs.
func TestCommandsReadTheSessionTheBoardNames(t *testing.T) {
	t.Run("the task's own generation", func(t *testing.T) {
		// Arrange
		board, _, worktree := commandBoard(t, "claude")
		writeCommandFixture(t, filepath.Join(worktree, ".claude", "commands", "review.md"), "x")
		if err := state.WriteConversation(board.Service.Store.Home.State, state.Conversation{ID: "task-1", Generation: "g1", Harness: "claude", Session: "session-1"}); err != nil {
			t.Fatal(err)
		}

		// Act
		answered := commandsIn(t, getCommands(board, "task=task-1&generation=g1&session=session-1&token=test-instance"))

		// Assert
		if want := []string{"review"}; !reflect.DeepEqual(answered, want) {
			t.Fatalf("commands = %v, want %v", answered, want)
		}
	})
	t.Run("the CFO's own harness", func(t *testing.T) {
		// Arrange
		board, profile, _ := commandBoard(t, "claude")
		writeCommandFixture(t, filepath.Join(profile, ".pi", "agent", "prompts", "plan.md"), "x")
		writeCommandFixture(t, filepath.Join(profile, ".claude", "commands", "review.md"), "x")
		registerNativeCFO(t, board.Service.Store.Home.State, "pi", "s1")

		// Act
		answered := commandsIn(t, getCommands(board, "cfo=1&token=test-instance"))

		// Assert
		if want := []string{"plan"}; !reflect.DeepEqual(answered, want) {
			t.Fatalf("commands = %v, want %v", answered, want)
		}
	})
}

// Claude Code offers a command in a subfolder of .claude/commands behind its
// folder's name and a colon, and a skill for each folder holding a SKILL.md.
func TestClaudeCommandsAreOfferedAsClaudeCodeNamesThem(t *testing.T) {
	// Arrange
	root := t.TempDir()
	writeCommandFixture(t, filepath.Join(root, "commands", "compact.md"), "x")
	writeCommandFixture(t, filepath.Join(root, "commands", "review.md"), "x")
	writeCommandFixture(t, filepath.Join(root, "commands", "frontend", "component.md"), "x")
	writeCommandFixture(t, filepath.Join(root, "commands", "frontend", "tokens", "audit.md"), "x")
	writeCommandFixture(t, filepath.Join(root, "skills", "deploy", "SKILL.md"), "x")
	writeCommandFixture(t, filepath.Join(root, "skills", "notes.txt"), "x")
	writeCommandFixture(t, filepath.Join(root, "skills", "not-a-skill", "README.md"), "x")

	// Act
	names := ownNames(uniqueSorted(claudeCommands(root)))

	// Assert
	if want := []string{"compact", "deploy", "frontend:component", "frontend:tokens:audit", "review"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("commands = %v, want %v", names, want)
	}
}

// A harness folder holding nothing offers the harness's built-ins alone, each
// as its harness describes it (Claude Code's command reference, Codex
// 0.160.0's slash_command.rs and pi 1.4.0's BUILTIN_SLASH_COMMANDS), and a
// harness without a table of built-ins offers nothing.
func TestAHarnessWithNoRecordOffersOnlyItsBuiltins(t *testing.T) {
	roots := harnessmap.Find(func(string) string { return "" }, t.TempDir())
	for _, c := range []struct {
		harness string
		builtin command
	}{
		{"claude", command{"compact", "Free up context by summarizing the conversation so far.", "builtin"}},
		{"codex", command{"model", "choose what model and reasoning effort to use", "builtin"}},
		{"pi", command{"quit", "Quit pi", "builtin"}},
		{"other", command{}},
	} {
		t.Run(c.harness, func(t *testing.T) {
			// Act
			entries := harnessCommands(commandSession{harness: c.harness, folder: t.TempDir()}, roots)

			// Assert
			if c.builtin == (command{}) {
				if len(entries) != 0 {
					t.Fatalf("offered %v, want none", entries)
				}
				return
			}
			for _, entry := range entries {
				if entry.Source != "builtin" {
					t.Fatalf("offered %+v from an empty folder", entry)
				}
			}
			if !slices.Contains(entries, c.builtin) {
				t.Fatalf("built-ins lack %+v", c.builtin)
			}
		})
	}
}

// Every harness with a table answers its built-ins after its own commands,
// each group by name, each built-in with the description its harness shows,
// and none its harness leaves out of its own list.
func TestEachHarnessAnswersItsBuiltinsAfterItsOwnCommands(t *testing.T) {
	for _, c := range []struct {
		harness    string
		builtin    command
		hidden     []string
		ownFixture string
	}{
		{"claude", command{"model", "Switch the AI model and save it as your default for new sessions.", "builtin"}, []string{"heapdump", "setup-bedrock", "setup-vertex", "vim", "pr-comments", "ultraplan", "batch", "deep-research", "plugin-authoring"}, filepath.Join(".claude", "commands", "ship.md")},
		{"codex", command{"setup-default-sandbox", "set up elevated agent sandbox", "builtin"}, []string{"rollout", "test-approval", "debug-m-drop", "debug-m-update", "elevate-sandbox"}, ""},
		{"pi", command{"quit", "Quit pi", "builtin"}, nil, filepath.Join(".pi", "prompts", "ship.md")},
	} {
		t.Run(c.harness, func(t *testing.T) {
			// Arrange
			board, _, worktree := commandBoard(t, c.harness)
			wantOwn := []string{}
			if c.ownFixture != "" {
				writeCommandFixture(t, filepath.Join(worktree, c.ownFixture), "x")
				wantOwn = []string{"ship"}
			}

			// Act
			entries := entriesIn(t, getCommands(board, "task=task-1&token=test-instance"))

			// Assert
			own := ownNames(entries)
			if !reflect.DeepEqual(own, wantOwn) {
				t.Fatalf("own commands = %v, want %v", own, wantOwn)
			}
			builtins := entries[len(own):]
			for i, entry := range builtins {
				if entry.Source != "builtin" || entry.Description == "" {
					t.Fatalf("built-in %+v lacks its source or description", entry)
				}
				if i > 0 && builtins[i-1].Name >= entry.Name {
					t.Fatalf("built-ins out of order at %q after %q", entry.Name, builtins[i-1].Name)
				}
				if slices.Contains(c.hidden, entry.Name) {
					t.Fatalf("answered %q, which the harness leaves out of its list", entry.Name)
				}
			}
			if !slices.Contains(builtins, c.builtin) {
				t.Fatalf("built-ins lack %+v", c.builtin)
			}
		})
	}
}

// A session's own command of a built-in's name replaces the built-in.
func TestAnOwnCommandHidesTheBuiltinOfTheSameName(t *testing.T) {
	// Arrange
	board, _, worktree := commandBoard(t, "claude")
	writeCommandFixture(t, filepath.Join(worktree, ".claude", "commands", "model.md"), "---\ndescription: Pick our model\n---\nx")

	// Act
	entries := entriesIn(t, getCommands(board, "task=task-1&token=test-instance"))

	// Assert
	named := slices.DeleteFunc(entries, func(entry command) bool { return entry.Name != "model" })
	if want := []command{{"model", "Pick our model", "command"}}; !reflect.DeepEqual(named, want) {
		t.Fatalf("model entries = %+v, want %+v", named, want)
	}
}

// Each own command and skill carries the description its frontmatter gives,
// and an empty one when it gives none.
func TestOwnCommandsCarryTheirFrontmatterDescriptions(t *testing.T) {
	for _, c := range []struct {
		harness string
		files   map[string]string
		want    []command
	}{
		{"claude", map[string]string{
			filepath.Join(".claude", "commands", "ship.md"):          "---\ndescription: Ship the branch\n---\nx",
			filepath.Join(".claude", "commands", "plain.md"):         "x",
			filepath.Join(".claude", "skills", "deploy", "SKILL.md"): "---\ndescription: Deploy the app\n---\nx",
		}, []command{{"deploy", "Deploy the app", "skill"}, {"plain", "", "command"}, {"ship", "Ship the branch", "command"}}},
		{"pi", map[string]string{
			filepath.Join(".pi", "prompts", "ship.md"):           "---\ndescription: Ship the branch\n---\nx",
			filepath.Join(".pi", "prompts", "plain.md"):          "x",
			filepath.Join(".pi", "skills", "deploy", "SKILL.md"): "---\ndescription: Deploy the app\n---\nx",
		}, []command{{"plain", "", "command"}, {"ship", "Ship the branch", "command"}, {"skill:deploy", "Deploy the app", "skill"}}},
	} {
		t.Run(c.harness, func(t *testing.T) {
			// Arrange
			board, _, worktree := commandBoard(t, c.harness)
			for name, body := range c.files {
				writeCommandFixture(t, filepath.Join(worktree, name), body)
			}

			// Act
			entries := entriesIn(t, getCommands(board, "task=task-1&token=test-instance"))

			// Assert
			if own := entries[:len(ownNames(entries))]; !reflect.DeepEqual(own, c.want) {
				t.Fatalf("own commands = %+v, want %+v", own, c.want)
			}
		})
	}
}

func TestCommandsHonorSkillMetadataAndPiPromptPaths(t *testing.T) {
	for _, harness := range []string{"claude", "pi"} {
		t.Run(harness, func(t *testing.T) {
			board, profile, worktree := commandBoard(t, harness)
			root := filepath.Join(worktree, ".claude")
			want := []string{"visible"}
			if harness == "pi" {
				root = filepath.Join(worktree, ".pi")
				want = []string{"custom", "skill:visible"}
				writeCommandFixture(t, filepath.Join(root, "settings.json"), `{"prompts":["../custom.md"]}`)
				writeCommandFixture(t, filepath.Join(worktree, "custom.md"), "Custom prompt")
				writeCommandFixture(t, filepath.Join(profile, ".pi", "agent", "skills", "invalid", "SKILL.md"), "No description")
			} else {
				writeCommandFixture(t, filepath.Join(root, "skills", "hidden", "SKILL.md"), "---\nuser-invocable: false\n---\nHidden")
			}
			writeCommandFixture(t, filepath.Join(root, "skills", "folder", "SKILL.md"), "---\nname: visible\ndescription: A skill\n---\nBody")
			answered := commandsIn(t, getCommands(board, "task=task-1&generation=g1&token=test-instance"))
			if !reflect.DeepEqual(answered, want) {
				t.Fatalf("commands = %v, want %v", answered, want)
			}
		})
	}
}

func TestCommandsRejectUnownedSessionAndNodeSelectors(t *testing.T) {
	board, _, _ := commandBoard(t, "claude")
	for _, selector := range []string{"session=other", "session=../../other.jsonl", "node=other", "path=C:/other"} {
		response := getCommands(board, "task=task-1&generation=g1&token=test-instance&"+selector)
		if response.Code == http.StatusOK {
			t.Fatalf("%s answered a parent's command list: %s", selector, response.Body)
		}
	}
}

func TestPiCommandExclusionsApplyToSharedAndNestedSkills(t *testing.T) {
	board, profile, _ := commandBoard(t, "pi")
	agentDir := filepath.Join(profile, ".pi", "agent")
	writeCommandFixture(t, filepath.Join(agentDir, "settings.json"), `{"skills":["!**/hidden/**","-skills/local","+skills/local"]}`)
	for _, path := range []string{filepath.Join(profile, ".agents", "skills", "hidden", "SKILL.md"), filepath.Join(agentDir, "skills", "local", "SKILL.md")} {
		writeCommandFixture(t, path, "---\ndescription: hidden\n---\nbody")
	}
	if commands := commandsIn(t, getCommands(board, "task=task-1&token=test-instance")); len(commands) != 0 {
		t.Fatalf("excluded skills shown: %v", commands)
	}
}

func TestPiCommandsDoNotCoerceInvalidSkillMetadata(t *testing.T) {
	board, _, worktree := commandBoard(t, "pi")
	writeCommandFixture(t, filepath.Join(worktree, ".pi", "skills", "valid", "SKILL.md"), "---\nname: 123\ndescription: A skill\n---\nbody")
	writeCommandFixture(t, filepath.Join(worktree, ".pi", "skills", "invalid", "SKILL.md"), "---\ndescription: 123\n---\nbody")
	if got := commandsIn(t, getCommands(board, "task=task-1&token=test-instance")); !reflect.DeepEqual(got, []string{"skill:valid"}) {
		t.Fatalf("commands=%v", got)
	}
}
