package janitor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/reap"
)

// chrome-devtools-axi makes a folder for each named session and removes none:
// 233 had gathered by 2026-10-09, each naming a bridge that no longer ran.
// The sweep removes a session's folder once its bridge is gone and a day has
// passed since anything wrote to it, and leaves one whose bridge runs, one
// used within the day, one that holds anything but the tool's own files, and
// one whose pid file it cannot read.
func TestSweepRemovesTheBrowserSessionsWhoseBridgeIsGone(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	sessions := filepath.Join(t.TempDir(), "sessions")
	session := func(name string, isOld bool, files map[string]string) string {
		path := filepath.Join(sessions, name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		for file, content := range files {
			if err := os.WriteFile(filepath.Join(path, file), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if isOld {
			makeOld(t, path)
		}
		return path
	}
	gone := session("gone", true, map[string]string{"bridge.pid": `{"pid":41001,"port":9301}`, "snapshot-generation": "4"})
	stopped := session("stopped", true, map[string]string{"snapshot-generation": "1", "selected-page-id": "2"})
	reused := session("reused", true, map[string]string{"bridge.pid": `{"pid":41003,"port":9303}`})
	running := session("running", true, map[string]string{"bridge.pid": `{"pid":41002,"port":9302}`})
	recent := session("recent", false, map[string]string{"bridge.pid": `{"pid":41004,"port":9304}`})
	foreign := session("foreign", true, map[string]string{"bridge.pid": `{"pid":41005,"port":9305}`, "notes.txt": "kept by a person"})
	unreadable := session("unreadable", true, map[string]string{"bridge.pid": "not a pid file"})
	cfg := f.config(reap.Inventory{Processes: []reap.Process{
		selfProcess(t),
		{PID: 41002, Name: "node.exe", CommandLine: `node C:\npm\node_modules\chrome-devtools-axi\dist\bin\chrome-devtools-axi-bridge.js`},
		// The pid a gone bridge had now names another program.
		{PID: 41003, Name: "notepad.exe", CommandLine: `notepad.exe`},
	}})
	cfg.BrowserSessions = sessions

	// Act
	record := Sweep(t.Context(), cfg)

	// Assert
	for _, path := range []string{gone, stopped, reused} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s is still there (%v), want it removed: notes %v", path, err, record.Notes)
		}
		if item, ok := has(record.Removed, path); !ok || item.Kind != "browser-session" {
			t.Errorf("the record's removals %+v do not name %s as a browser session", record.Removed, path)
		}
	}
	for _, path := range []string{running, recent, foreign, unreadable} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was removed (%v), want it kept", path, err)
		}
	}
}

// A sweep that cannot prove it read the process list knows of no bridge that
// runs, so it removes no session's folder.
func TestSweepRemovesNoBrowserSessionWithoutAProvenProcessList(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	sessions := filepath.Join(t.TempDir(), "sessions")
	gone := filepath.Join(sessions, "gone")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gone, "bridge.pid"), []byte(`{"pid":41001,"port":9301}`), 0o600); err != nil {
		t.Fatal(err)
	}
	makeOld(t, gone)
	cfg := f.config(reap.Inventory{})
	cfg.BrowserSessions = sessions

	// Act
	record := Sweep(t.Context(), cfg)

	// Assert
	if _, err := os.Stat(gone); err != nil {
		t.Errorf("the session folder was removed on an unproven process list (%v): removed %+v", err, record.Removed)
	}
}
