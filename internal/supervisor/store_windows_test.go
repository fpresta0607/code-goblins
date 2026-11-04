package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// A pass that stops short still says how many events it set aside, since
// they have left the inbox all the same: here a later event is held open
// without delete sharing, as a virus scanner holds a file it reads, so it
// cannot be removed.
func TestAnIntakeThatStopsShortStillSaysWhatItSetAside(t *testing.T) {
	// Arrange
	s, h := testStore(t)
	stale := event(t, h, "UserPromptSubmit", "s1", "turn", time.Now().Add(-2*time.Hour))
	stale.TaskID = "retired-task"
	started := event(t, h, "SessionStart", "s2", "", time.Now())
	for _, e := range []nativehook.Event{stale, started} {
		if err := nativehook.Spool(h.State, e); err != nil {
			t.Fatal(err)
		}
	}
	name, err := windows.UTF16PtrFromString(filepath.Join(nativehook.SpoolDir(h.State), started.ID+".event.json"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(handle) })

	// Act
	_, err = s.Ingest()

	// Assert
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("Ingest = %v, want it stopped by the held event", err)
	}
	if issues := s.Snapshot().Issues; len(issues) != 1 || !strings.Contains(issues[0], "set aside 1 native events that waited for evidence that does not come") {
		t.Fatalf("issues %q, want one saying the stale event was set aside", issues)
	}
}

// An event that leaves the inbox after an intake listed it has left it, and
// the intake takes the rest: on 2026-10-27 the CFO moved the inbox's events
// to a quarantine folder while the supervisor read them, and each intake
// stopped on the missing file, an error the supervisor met again hours later
// (#1625). Here the first event is held without read sharing, so the intake
// waits on it after its listing while the next one is moved away, and an
// event spooled during that wait proves the listing came first.
func TestAnIntakeTakesTheRestWhenAnEventLeavesTheInboxAfterItsListing(t *testing.T) {
	// Arrange
	s, h := testStore(t)
	inbox := nativehook.SpoolDir(h.State)
	spool := func(name, session string) nativehook.Event {
		t.Helper()
		e := event(t, h, "SessionStart", session, "", time.Now())
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(inbox, name), append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		return e
	}
	held := spool("1-held.event.json", "s1")
	spool("2-moved.event.json", "s2")
	name, err := windows.UTF16PtrFromString(filepath.Join(inbox, "1-held.event.json"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	release := sync.OnceFunc(func() { windows.CloseHandle(handle) })
	t.Cleanup(release)
	quarantine := filepath.Join(h.State, "native-inbox-quarantine")
	if err := os.Mkdir(quarantine, 0700); err != nil {
		t.Fatal(err)
	}

	// Act
	type intake struct {
		accepted []nativehook.Event
		err      error
	}
	done := make(chan intake, 1)
	go func() {
		accepted, err := s.Ingest()
		done <- intake{accepted, err}
	}()
	time.Sleep(time.Second)
	late := spool("3-late.event.json", "s3")
	if err := os.Rename(filepath.Join(inbox, "2-moved.event.json"), filepath.Join(quarantine, "2-moved.event.json")); err != nil {
		t.Fatal(err)
	}
	release()
	var got intake
	select {
	case got = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the intake did not end")
	}

	// Assert
	if slices.ContainsFunc(got.accepted, func(e nativehook.Event) bool { return e.ID == late.ID }) {
		t.Fatal("the intake listed the inbox only after the event left it, so it never met the missing file")
	}
	if got.err != nil {
		t.Fatalf("Ingest = %v, want it to pass over the event that left the inbox", got.err)
	}
	if len(got.accepted) != 1 || got.accepted[0].ID != held.ID {
		t.Fatalf("accepted %+v, want only the held event", got.accepted)
	}
}
