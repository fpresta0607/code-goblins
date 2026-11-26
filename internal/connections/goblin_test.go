package connections

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// goblinFixture is a Claude goblin of the made-up project acme with its
// session record, which a test writes the lines of.
type goblinFixture struct {
	*testEnvironment
	meta       state.TaskMeta
	transcript string
}

func newGoblinFixture(t *testing.T) goblinFixture {
	t.Helper()
	e := newTestEnvironment(t)
	e.manifest(t, "acme", `[{"name":"sample","method":"env","env":["ACME_SAMPLE"]},{"name":"other","method":"env","env":["ACME_OTHER"]}]`)
	path := e.checkout(t, "acme")
	writeFile(t, path+"/.mcp.json", `{"mcpServers":{"acme-valued":{"command":"acme-valued","env":{"ACME_MODE":"literal"}}}}`)
	meta := state.TaskMeta{ID: "acme-task", Project: path, Worktree: path, Harness: "claude", SpawnGen: "g1", HasCredentials: true, Credentials: []string{"sample"}}
	return goblinFixture{e, meta, filepath.Join(filepath.Dir(e.DataDir), "session.jsonl")}
}

func (f goblinFixture) write(t *testing.T, text string) {
	t.Helper()
	writeFile(t, f.transcript, text)
}

func (f goblinFixture) append(t *testing.T, text string) {
	t.Helper()
	file, err := os.OpenFile(f.transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func (f goblinFixture) size(t *testing.T) int64 {
	t.Helper()
	info, err := os.Stat(f.transcript)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func (f goblinFixture) read(t *testing.T, from int64, prior Goblin) (Goblin, int64) {
	t.Helper()
	return f.ReadGoblin(f.meta, f.transcript, from, prior)
}

func names(items []Item, group string) []string {
	var found []string
	for _, item := range items {
		if item.Group == group {
			found = append(found, item.Name)
		}
	}
	return found
}

// A goblin's level shows what its own session record says: a server that
// loaded, with the tools it gave, and one that failed to load, with its error.
func TestGoblinReadsTheLoadedAndFailedServersFromItsTranscript(t *testing.T) {
	// Arrange
	f := newGoblinFixture(t)
	f.write(t, transcriptLine(t, map[string]any{"type": "user", "message": "hello"})+
		deltaLine(t,
			[]string{"mcp__acme-docs__search", "mcp__acme-docs__fetch", "mcp__claude-in-chrome__navigate", "Bash"}, nil,
			[]map[string]string{{"name": "globex-db", "errorCode": "connection_failed", "error": "connection refused by db.globex.example"}},
			nil))

	// Act
	goblin, offset := f.read(t, 0, Goblin{})

	// Assert
	loaded := find(t, goblin.Items, "loaded:acme-docs")
	if loaded.Status != StatusVerified || loaded.Group != GroupLoaded || loaded.Level != LevelGoblin || !slices.Equal(loaded.Tools, []string{"fetch", "search"}) {
		t.Errorf("acme-docs = %q %v, want it verified with its two tools", loaded.Status, loaded.Tools)
	}
	failed := find(t, goblin.Items, "failed:globex-db")
	if failed.Status != StatusFailed || failed.Group != GroupFailed || failed.Detail != "connection refused by db.globex.example" {
		t.Errorf("globex-db = %q %q, want it failed with its error", failed.Status, failed.Detail)
	}
	if slices.Contains(ids(goblin.Items), "loaded:claude-in-chrome") {
		t.Error("Claude's own browser connector was listed as the goblin's")
	}
	if !goblin.IsConfirmed || goblin.Task != "acme-task" || goblin.Project != "acme" || goblin.Generation != "g1" || offset != f.size(t) {
		t.Errorf("goblin = %+v at %d, want it confirmed, of acme, read through the file's end %d", goblin, offset, f.size(t))
	}
}

// A goblin's level also says what its task record gives it: the services it
// carries or is not given, and the project servers held for a brief.
func TestGoblinShowsWhatItsTaskRecordGivesIt(t *testing.T) {
	// Arrange
	f := newGoblinFixture(t)
	f.meta.MCPServers = nil

	// Act
	goblin, _ := f.read(t, 0, Goblin{})
	f.meta.MCPServers = []string{"acme-valued"}
	granted, _ := f.read(t, 0, Goblin{})

	// Assert
	if got := find(t, goblin.Items, "service:sample"); got.Group != GroupCarried || got.Grant != "revoke" {
		t.Errorf("sample = %q to %q, want it carried and revocable", got.Group, got.Grant)
	}
	if got := find(t, goblin.Items, "service:other"); got.Group != GroupNotGiven || got.Grant != "grant" || got.Status != StatusNotGiven {
		t.Errorf("other = %q to %q, want it not given and grantable", got.Group, got.Grant)
	}
	if got := find(t, goblin.Items, "mcp:acme-valued"); got.Group != GroupNotGiven || got.Grant != "grant" {
		t.Errorf("acme-valued = %q to %q, want the held server not given", got.Group, got.Grant)
	}
	if got := find(t, granted.Items, "mcp:acme-valued"); got.Group != GroupCarried || got.Grant != "revoke" {
		t.Errorf("granted acme-valued = %q to %q, want it carried after its grant", got.Group, got.Grant)
	}
	if goblin.IsConfirmed || !strings.Contains(goblin.Note, "has not recorded") {
		t.Errorf("goblin with no record = confirmed %v, note %q, want it unconfirmed and told why", goblin.IsConfirmed, goblin.Note)
	}
}

// A read from an offset reads only the lines appended after it: the servers
// the earlier lines said are carried forward, not read again. The earlier bytes
// are overwritten here, so a read that started over could not find them.
func TestGoblinReadsOnlyTheLinesAppendedAfterItsOffset(t *testing.T) {
	// Arrange
	f := newGoblinFixture(t)
	first := deltaLine(t, []string{"mcp__acme-docs__search", "mcp__acme-docs__fetch"}, nil, nil, nil)
	f.write(t, first)
	before, offset := f.read(t, 0, Goblin{})
	f.write(t, strings.Repeat(" ", len(first)-1)+"\n")
	f.append(t, deltaLine(t, []string{"mcp__acme-docs__list", "mcp__globex-db__query"}, []string{"mcp__acme-docs__search"}, nil, nil))

	// Act
	after, next := f.read(t, offset, before)

	// Assert
	docs := find(t, after.Items, "loaded:acme-docs")
	if !slices.Equal(docs.Tools, []string{"fetch", "list"}) {
		t.Errorf("acme-docs tools = %v, want the first read's fetch kept, search removed and list added", docs.Tools)
	}
	if !slices.Equal(names(after.Items, GroupLoaded), []string{"acme-docs", "globex-db"}) || next != f.size(t) || next <= offset {
		t.Errorf("loaded %v, offset %d (size %d), want both servers and the offset at the new end", names(after.Items, GroupLoaded), next, f.size(t))
	}
}

// Only whole lines are read: a line still being written is read when it is
// whole, never half.
func TestGoblinLeavesAHalfWrittenLineForTheNextRead(t *testing.T) {
	// Arrange
	f := newGoblinFixture(t)
	f.write(t, deltaLine(t, []string{"mcp__acme-docs__search"}, nil, nil, nil))
	first, offset := f.read(t, 0, Goblin{})
	whole := deltaLine(t, []string{"mcp__globex-db__query"}, nil, nil, nil)
	f.append(t, whole[:len(whole)/2])

	// Act
	partial, partialOffset := f.read(t, offset, first)
	f.append(t, whole[len(whole)/2:])
	finished, finishedOffset := f.read(t, partialOffset, partial)

	// Assert
	if partialOffset != offset || !slices.Equal(names(partial.Items, GroupLoaded), []string{"acme-docs"}) {
		t.Errorf("half-written read = %v at %d, want nothing new and the offset kept at %d", names(partial.Items, GroupLoaded), partialOffset, offset)
	}
	if !slices.Equal(names(finished.Items, GroupLoaded), []string{"acme-docs", "globex-db"}) || finishedOffset != f.size(t) {
		t.Errorf("finished read = %v at %d, want both servers and the offset at the end", names(finished.Items, GroupLoaded), finishedOffset)
	}
}

// A session record that is shorter than the offset was replaced: it is read
// again from the start and nothing of the earlier record is carried.
func TestGoblinReadsATruncatedTranscriptFromTheStart(t *testing.T) {
	// Arrange
	f := newGoblinFixture(t)
	f.write(t, deltaLine(t, []string{"mcp__acme-docs__search", "mcp__acme-docs__fetch", "mcp__acme-docs__list"}, nil, nil, nil)+
		deltaLine(t, []string{"mcp__globex-db__query"}, nil, nil, nil))
	before, offset := f.read(t, 0, Goblin{})
	replacement := deltaLine(t, []string{"mcp__globex-mail__send"}, nil, nil, nil)
	f.write(t, replacement)
	if f.size(t) >= offset {
		t.Fatal("the replacement is not shorter than the offset")
	}

	// Act
	after, next := f.read(t, offset, before)

	// Assert
	if !slices.Equal(names(after.Items, GroupLoaded), []string{"globex-mail"}) || next != int64(len(replacement)) {
		t.Fatalf("loaded %v at %d, want only the replacement's server read through its end %d", names(after.Items, GroupLoaded), next, len(replacement))
	}
}

// The latest delta's failed and pending lists replace the earlier ones, and a
// failed server is not shown as loaded.
func TestGoblinTakesTheLatestFailedAndPendingServers(t *testing.T) {
	// Arrange
	f := newGoblinFixture(t)
	f.write(t,
		deltaLine(t, []string{"mcp__acme-docs__search"}, nil, []map[string]string{{"name": "acme-docs", "error": "timed out"}, {"name": "globex-db", "errorCode": "auth_failed"}}, []string{"globex-mail"})+
			deltaLine(t, nil, nil, []map[string]string{{"name": "globex-db", "errorCode": "auth_failed"}}, []string{"globex-mail"}))

	// Act
	goblin, _ := f.read(t, 0, Goblin{})

	// Assert
	if !slices.Equal(names(goblin.Items, GroupFailed), []string{"globex-db"}) || find(t, goblin.Items, "failed:globex-db").Detail != "auth_failed" {
		t.Errorf("failed %v, want only the latest delta's globex-db, with its error code for its words", names(goblin.Items, GroupFailed))
	}
	if !slices.Equal(names(goblin.Items, GroupLoaded), []string{"acme-docs", "globex-mail"}) || find(t, goblin.Items, "pending:globex-mail").Status != StatusNotChecked {
		t.Errorf("loaded %v, want acme-docs loaded again and globex-mail still starting", names(goblin.Items, GroupLoaded))
	}
}

// A goblin of a harness that does not record its servers says so, and reads
// no session record whatever it is given.
func TestGoblinOfAnotherHarnessSaysItsSessionDoesNotRecordServers(t *testing.T) {
	for _, test := range []struct{ harness, note string }{
		{"codex", "Codex does not record"},
		{"pi", "Pi loads no MCP servers"},
		{"made-up", "does not record which servers loaded"},
	} {
		t.Run(test.harness, func(t *testing.T) {
			// Arrange
			f := newGoblinFixture(t)
			f.meta.Harness = test.harness
			f.write(t, deltaLine(t, []string{"mcp__acme-docs__search"}, nil, nil, nil))

			// Act
			goblin, offset := f.read(t, 0, Goblin{})

			// Assert
			if !strings.Contains(goblin.Note, test.note) || goblin.IsConfirmed || offset != 0 || len(names(goblin.Items, GroupLoaded)) != 0 {
				t.Fatalf("goblin = %q confirmed %v at %d with %v, want its note and no server read", goblin.Note, goblin.IsConfirmed, offset, names(goblin.Items, GroupLoaded))
			}
		})
	}
}

// A failure's words quote addresses and headers: what could be a credential in
// them is cut before they are shown.
func TestGoblinFailureTextKeepsNoCredentialFromAnAddressOrHeader(t *testing.T) {
	cases := []struct{ name, reason, secret string }{
		{"sign-in part of an address", "cannot reach https://acme-user:pw-5f2a@mcp.acme.example/mcp", "pw-5f2a"},
		{"query of an address", "cannot reach https://mcp.acme.example/mcp?token=query-9c1d&x=1", "query-9c1d"},
		{"bearer header", "rejected Authorization: Bearer header-secret-77aa", "header-secret-77aa"},
		{"named value", "failed with api_key=named-3b8e", "named-3b8e"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Act
			text := failureText(test.reason)

			// Assert
			if strings.Contains(text, test.secret) || text == "" {
				t.Fatalf("failure text %q still holds %q", text, test.secret)
			}
		})
	}
	if long := failureText(strings.Repeat("a ", 400)); len([]rune(long)) > maxFailureText {
		t.Errorf("a long failure is %d characters, want at most %d", len([]rune(long)), maxFailureText)
	}
	if got := failureText("  "); got != "It failed to load." {
		t.Errorf("an empty failure reads %q, want a plain line", got)
	}
}

// Reading a goblin starts no process and opens no credential store.
func TestGoblinReadStartsNothing(t *testing.T) {
	// Arrange
	f := newGoblinFixture(t)
	f.OpenStore = nil
	f.LookPath = nil
	f.UserEnvNames = nil
	f.write(t, deltaLine(t, []string{"mcp__acme-docs__search"}, nil, nil, nil))

	// Act
	f.read(t, 0, Goblin{})

	// Assert
	if f.runner.total() != 0 {
		t.Fatalf("reading a goblin started %d processes", f.runner.total())
	}
}
