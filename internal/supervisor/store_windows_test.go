package supervisor

import (
	"errors"
	"path/filepath"
	"strings"
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
