package supervisor

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// cardPathFixture is a board with a step card whose step names a file, a
// question that names a folder with a space in its name, a goblin's wait
// that names the file, and a run card, and a handler whose File Explorer is
// recorded instead of started.
func cardPathFixture(t *testing.T) (*HTTP, *editorStarter, string, string) {
	t.Helper()
	store, h := testStore(t)
	folder := filepath.Join(h.Root, "pd proxy")
	file := filepath.Join(h.Root, "data", "iproyal-refund.md")
	for _, dir := range []string{folder, filepath.Dir(file)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(file, []byte("Please refund order 4412."), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store.db.Credentials = []CredentialRequest{{ID: "cred-1111111111111111", Project: "pd-proxy", Why: "Send the IPRoyal refund email", Steps: []string{"Send the email in " + file + ".", "Press Done"}, State: "open", CreatedAt: now}}
	store.db.Questions = []Question{{ID: "notify-task-1-7", Text: "Keep the proxy notes in `" + folder + "`?", Options: []string{"Keep them", "Move them"}, Status: "pending", CreatedAt: now}}
	store.db.Reviews = []Review{{ID: "waiting-task-1-9", Task: "task-1", Title: "Waiting on you: send the refund email in " + file, State: "open", CreatedAt: now}}
	store.db.Runs = []Run{{ID: "run-task-1-8", Title: "Sign in to IPRoyal", Shell: "powershell", Command: "iproyal login", Cwd: h.Root, State: "ready", CreatedAt: now}}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	starter := &editorStarter{}
	handler := NewHTTP(&Service{Store: store, Instance: "instance"}, "board.local", nil)
	handler.editor = starter
	return handler, starter, file, folder
}

func postCardPath(handler *HTTP, body string) (int, string) {
	r := httptest.NewRequest("POST", "http://board.local/api/cards/open-path", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://board.local")
	r.Header.Set("X-CFO-Token", "instance")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func cardPathBody(t *testing.T, kind, id, path string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"kind": kind, "id": id, "path": path})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// A path a card names opens File Explorer at its folder: a file selected in
// its folder, a folder opened itself, each as one argument.
func TestOpenCardPathOpensFileExplorerAtAPathTheCardNames(t *testing.T) {
	explorer := filepath.Join(os.Getenv("SystemRoot"), "explorer.exe")
	for _, c := range []struct{ kind, id string }{
		{"credential", "cred-1111111111111111"},
		{"question", "notify-task-1-7"},
		{"review", "waiting-task-1-9"},
		{"run", "run-task-1-8"},
	} {
		t.Run(c.kind, func(t *testing.T) {
			// Arrange
			handler, starter, file, folder := cardPathFixture(t)
			path, want := file, []string{"/select,", file}
			switch c.kind {
			case "question":
				path, want = folder, []string{folder}
			case "run":
				path, want = handler.Service.Store.Home.Root, []string{handler.Service.Store.Home.Root}
			}

			// Act
			code, body := postCardPath(handler, cardPathBody(t, c.kind, c.id, path))

			// Assert
			if code != 200 {
				t.Fatalf("open = %d %s, want it opened", code, body)
			}
			if len(starter.requests) != 1 || starter.requests[0].Name != explorer || !slices.Equal(starter.requests[0].Args, want) {
				t.Fatalf("started %+v, want %s %q", starter.requests, explorer, want)
			}
		})
	}
}

// The browser names a card and a path, and nothing opens unless that card's
// own text names that exact path, on a drive of this PC, and it exists.
func TestOpenCardPathRefusesAPathTheCardDoesNotName(t *testing.T) {
	// Arrange
	handler, starter, file, folder := cardPathFixture(t)
	for name, c := range map[string]struct {
		kind, id, path string
		code           int
	}{
		"a card that is gone":           {"credential", "cred-2222222222222222", file, 404},
		"a kind of card with no links":  {"browser-signin", "cred-1111111111111111", file, 404},
		"another card's path":           {"credential", "cred-1111111111111111", folder, 400},
		"a folder above the named file": {"credential", "cred-1111111111111111", filepath.Dir(file), 400},
		"a program":                     {"credential", "cred-1111111111111111", `C:\Windows\System32\cmd.exe`, 400},
		"a page":                        {"credential", "cred-1111111111111111", "https://iproyal.com/refund", 400},
		"a relative path":               {"credential", "cred-1111111111111111", `data\iproyal-refund.md`, 400},
		"a network share":               {"credential", "cred-1111111111111111", `\\fileserver\share\notes.md`, 400},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			code, body := postCardPath(handler, cardPathBody(t, c.kind, c.id, c.path))

			// Assert
			if code != c.code {
				t.Fatalf("open = %d %s, want %d", code, body, c.code)
			}
		})
	}
	if len(starter.requests) != 0 {
		t.Fatalf("a refused path started %+v", starter.requests)
	}
}

// A card can name a path that is gone by the time he clicks it, or one with
// a comma, which File Explorer would read as the end of its argument; neither
// starts it.
func TestOpenCardPathRefusesANamedPathThatCannotOpen(t *testing.T) {
	// Arrange
	handler, starter, file, _ := cardPathFixture(t)
	comma := filepath.Join(filepath.Dir(file), "refund,old.md")
	if err := os.WriteFile(comma, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := handler.Service.Store
	store.db.Credentials[0].Steps = append(store.db.Credentials[0].Steps, "Read "+comma, "Then read `"+file+".gone`")
	if err := store.save(); err != nil {
		t.Fatal(err)
	}

	// Act
	commaCode, commaBody := postCardPath(handler, cardPathBody(t, "credential", "cred-1111111111111111", comma))
	goneCode, goneBody := postCardPath(handler, cardPathBody(t, "credential", "cred-1111111111111111", file+".gone"))

	// Assert
	if commaCode != 400 || !strings.Contains(commaBody, "A path holding a comma or a quote") || goneCode != 404 || !strings.Contains(goneBody, "Nothing is at that path now") {
		t.Fatalf("comma = %d %s, gone = %d %s; want each refused for itself", commaCode, commaBody, goneCode, goneBody)
	}
	if len(starter.requests) != 0 {
		t.Fatalf("a path that cannot open started %+v", starter.requests)
	}
}

// A run card names the folder it runs in, which comes from its request and
// not from text, so it is the one way a path that is no drive letter's can
// reach the card's paths: a share, a device path or a drive's current
// folder. None of them opens File Explorer.
func TestOpenCardPathRefusesARunFolderOffTheDrivesOfThisPC(t *testing.T) {
	for name, folder := range map[string]string{
		"a network share":          `\\fileserver\share\pd proxy`,
		"a device path":            `\\?\C:\pd proxy`,
		"a drive's current folder": `C:pd proxy`,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			handler, starter, _, _ := cardPathFixture(t)
			store := handler.Service.Store
			store.db.Runs[0].Cwd = folder
			if err := store.save(); err != nil {
				t.Fatal(err)
			}

			// Act
			code, body := postCardPath(handler, cardPathBody(t, "run", "run-task-1-8", folder))

			// Assert
			if code != 400 || !strings.Contains(body, "Only a path on a drive of this PC") {
				t.Fatalf("open = %d %s, want it refused as no drive's path", code, body)
			}
			if len(starter.requests) != 0 {
				t.Fatalf("a refused folder started %+v", starter.requests)
			}
		})
	}
}

// Windows reads a path whatever its letter case, so a link whose card wrote
// the drive letter small opens the same file as one written large.
func TestOpenCardPathOpensAPathNamedInAnotherCase(t *testing.T) {
	// Arrange
	handler, starter, file, _ := cardPathFixture(t)
	store := handler.Service.Store
	store.db.Credentials[0].Steps = []string{"Send the email in " + strings.ToLower(file[:1]) + file[1:] + "."}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}

	// Act
	code, body := postCardPath(handler, cardPathBody(t, "credential", "cred-1111111111111111", strings.ToUpper(file[:1])+file[1:]))

	// Assert
	if code != 200 || len(starter.requests) != 1 {
		t.Fatalf("open = %d %s, started %+v; want the file opened", code, body, starter.requests)
	}
}

// The board finds a card's paths as its text shows them: up to a space, less
// the punctuation that ends a sentence, a closing bracket only when the path
// opened none, or a whole code value.
func TestNamedPathsFollowTheBoardsLinks(t *testing.T) {
	for text, want := range map[string][]string{
		`Steps point at C:\dev\code-goblins\data\pd-proxy-primary\iproyal-refund.md.`: {`C:\dev\code-goblins\data\pd-proxy-primary\iproyal-refund.md`},
		"Open `C:\\Users\\fpres\\OneDrive - SIQstack\\Desktop` (on his PC)":           {`C:\Users\fpres\OneDrive`, `C:\Users\fpres\OneDrive - SIQstack\Desktop`},
		`See (D:/CodeGoblins/scratch), then E:\notes, and https://example.com/C:\x`:   {`D:/CodeGoblins/scratch`, `E:\notes`},
		`No path here: ratio 3:4 and a:b`:                                             nil,
		`Install it in C:\apps\tool(v2), not (C:\apps\old).`:                          {`C:\apps\tool(v2)`, `C:\apps\old`},
	} {
		if got := namedPaths(text); !slices.Equal(got, want) {
			t.Errorf("namedPaths(%q) = %q, want %q", text, got, want)
		}
	}
}
