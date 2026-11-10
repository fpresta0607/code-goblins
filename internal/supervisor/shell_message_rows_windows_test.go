package supervisor

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
)

// His ! message runs as a line naming powershell.exe and a script the board
// keeps, so Claude Code records that line, not his command, as the shell row
// it ran and as the prompt it holds during a turn. The board shows each as
// the command he typed, and names the one still waiting by his message, so
// the chat never shows a script's path or his message twice (#2051).
func TestHisShellMessageShowsAsHisCommandWhileItWaitsAndOnceItRan(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	userHome := t.TempDir()
	meta := treeGoblin(t, h.State, userHome, "talker", true)
	created := time.Now().UTC().Add(-time.Minute)
	ran, _, err := shellMessageLine(h.State, "ran", meta.ID, "! Get-Location")
	if err != nil {
		t.Fatal(err)
	}
	queued, _, err := shellMessageLine(h.State, "queued", meta.ID, "! Get-Date | Select-Object -First 1")
	if err != nil {
		t.Fatal(err)
	}
	store.db.Actions = append(store.db.Actions,
		Action{ID: "ran", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "! Get-Location", Status: "succeeded", CreatedAt: created, UpdatedAt: created},
		Action{ID: "queued", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "! Get-Date | Select-Object -First 1", Status: "running", CreatedAt: created, UpdatedAt: created})
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	quoted := func(text string) string {
		data, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	appendEntries(t, filepath.Join(userHome, ".claude", "projects", "C--work-talker", "talker-conversation.jsonl"),
		`{"type":"user","uuid":"u1","timestamp":"`+stamp+`","message":{"role":"user","content":`+quoted("<bash-input>"+strings.TrimPrefix(ran, "! ")+"</bash-input>")+`}}`,
		`{"type":"queue-operation","operation":"enqueue","timestamp":"`+stamp+`","content":`+quoted(strings.TrimPrefix(queued, "! "))+`}`)
	service := &Service{Store: store, Instance: "board-token", Options: Options{Tree: &fleettree.Reader{Home: userHome}}}
	service.readTrees(context.Background())
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "http://127.0.0.1:50000/api/messages?task="+meta.ID+"&generation="+meta.SpawnGen+"&token=board-token", nil)
	request.RemoteAddr = "127.0.0.1:50001"

	// Act
	NewHTTP(service, "127.0.0.1:50000", nil).ServeHTTP(response, request)

	// Assert
	var page fleettree.MessagePage
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatalf("GET /api/messages = %d %s", response.Code, response.Body)
	}
	if len(page.Messages) == 0 {
		t.Fatalf("GET /api/messages served no messages: %s", response.Body)
	}
	if last := page.Messages[len(page.Messages)-1]; last.Source != fleettree.FromShell || last.Text != "Get-Location" {
		t.Errorf("messages = %+v; want his shell row last, reading Get-Location", page.Messages)
	}
	if len(page.Waiting) != 1 || page.Waiting[0].Source != fleettree.FromShell || page.Waiting[0].Text != "Get-Date | Select-Object -First 1" || page.Waiting[0].Action != "queued" {
		t.Errorf("waiting = %+v; want his queued command, named by his message", page.Waiting)
	}
	if strings.Contains(response.Body.String(), "shell-commands") {
		t.Errorf("the page names a script: %s", response.Body)
	}
}

// Only a line naming a script in the board's own folder of them reads as one
// of his shell commands; any other ! command shows as it was recorded.
func TestOnlyALineNamingTheBoardsOwnScriptReadsAsHisCommand(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	line, _, err := shellMessageLine(stateDir, "mine", "", "! Get-Date")
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := strings.Replace(line, filepath.ToSlash(stateDir), "C:/elsewhere", 1)
	for _, test := range []struct {
		name, text, want string
		isShell          bool
	}{
		{"the line it typed", line, "Get-Date", true},
		{"the line as recorded, without its !", strings.TrimPrefix(line, "! "), "Get-Date", true},
		{"a script elsewhere", elsewhere, "", false},
		{"a command of his own", "powershell.exe -File C:/tools/build.ps1", "", false},
		{"a script the folder no longer holds", strings.Replace(line, shellScriptName("mine"), shellScriptName("gone"), 1), "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			command, isShell := shellCommandOf(stateDir, test.text)

			// Assert
			if command != test.want || isShell != test.isShell {
				t.Errorf("shellCommandOf(%q) = %q, %v; want %q, %v", test.text, command, isShell, test.want, test.isShell)
			}
		})
	}
}
