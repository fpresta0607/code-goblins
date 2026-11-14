package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// devDriveMissingService is a board whose home's heavy folders are on D:,
// made from a file in this test's own folder, while D: is not attached: the
// machine started bootAge ago, the attach item was last made offeredAge
// before that, and start is its startup task's start.
func devDriveMissingService(t *testing.T, bootAge, offeredAge time.Duration, start func(context.Context) error) (*Service, *devdrive.Machine, string) {
	t.Helper()
	machine := devDriveMachine()
	s := devDriveService(t, &machine)
	boot := time.Now().Add(-bootAge)
	vhd := filepath.Join(t.TempDir(), "CodeGoblins.vhdx")
	s.Options.DevDrive.Boot = func() time.Time { return boot }
	s.Options.DevDrive.StartTask = start
	config := home.DevDriveConfig{Root: `D:\CodeGoblins`, VHD: vhd, MovedAt: boot.Add(-48 * time.Hour), Offered: map[string]time.Time{"attach": boot.Add(-offeredAge).UTC()}}
	if err := home.WriteDevDriveConfig(s.Store.Home.Root, config); err != nil {
		t.Fatal(err)
	}
	return s, &machine, vhd
}

// writeAttachResult writes what the startup task writes when it ends.
func writeAttachResult(t *testing.T, vhd string, isAttached bool) {
	t.Helper()
	now := time.Now().UTC()
	data := fmt.Sprintf(`{"started": %q, "finished": %q, "attached": %t, "letter": "D:", "tries": 12, "errors": ["try 12: The device is not ready."]}`,
		now.Format(time.RFC3339Nano), now.Add(time.Second).Format(time.RFC3339Nano), isAttached)
	if err := os.WriteFile(devdrive.AttachResultPath(vhd), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// After a boot, a missing drive is looked after at the first look, whatever
// the age of the last attach item: the board starts the drive's startup task,
// which needs no click, and once the task ended without attaching it, one
// attach item comes, once for the boot.
func TestAMissingDriveAfterABootIsLookedAfterAtOnce(t *testing.T) {
	for _, offeredAge := range []time.Duration{time.Minute, 3 * time.Hour, 40 * time.Hour} {
		t.Run(fmt.Sprint("last offer ", offeredAge, " before the boot"), func(t *testing.T) {
			// Arrange
			var starts int
			s, _, vhd := devDriveMissingService(t, 2*time.Minute, offeredAge, func(context.Context) error { starts++; return nil })

			// Act
			wait := s.keepDevDrive(context.Background())

			// Assert: the startup task, no click, and another look within a minute.
			view := s.devDriveViewNow()
			if starts != 1 || len(devDriveItems(s)) != 0 || wait != time.Minute || view.Line != devdrive.MissingLine(`D:\CodeGoblins`, devdrive.AttachByTask) || view.Action != "" {
				t.Fatalf("starts = %d, items = %+v, next look in %s, view = %+v; want the task started", starts, devDriveItems(s), wait, view)
			}

			// Act: the task ended and D: is still not there.
			writeAttachResult(t, vhd, false)
			s.keepDevDrive(context.Background())
			s.keepDevDrive(context.Background())

			// Assert: one attach item, and the task is not started again.
			items := devDriveItems(s)
			if starts != 1 || len(items) != 1 || items[0].DevDrive != "attach" || !items[0].Admin {
				t.Fatalf("starts = %d, items = %+v; want one attach item", starts, items)
			}
			if view := s.devDriveViewNow(); view.Waiting != "attach" || view.Line != devdrive.MissingLine(`D:\CodeGoblins`, devdrive.AttachByItem) {
				t.Errorf("view = %+v, want the attach item named while it waits", view)
			}
		})
	}
}

// A startup task the board may not start, as one an install from before this
// build registered, gets the attach item at the first look, which registers
// the task this build makes. It is made once for the boot, and once it ran
// nothing more comes.
func TestATaskTheBoardMayNotStartGetsTheAttachItemOnce(t *testing.T) {
	// Arrange
	var starts int
	s, machine, vhd := devDriveMissingService(t, time.Minute, time.Hour, func(context.Context) error {
		starts++
		return errors.New("schtasks /Run: ERROR: Access is denied.")
	})

	// Act
	s.keepDevDrive(context.Background())

	// Assert
	items := devDriveItems(s)
	if starts != 1 || len(items) != 1 || items[0].DevDrive != "attach" || items[0].Command != devdrive.CreateScript(vhd, "D:", 0) {
		t.Fatalf("starts = %d, items = %+v; want one attach item that runs the create script, which registers this build's task", starts, items)
	}

	// Act: it is not run, and the board looks again.
	endDevDriveItem(t, s, 1)
	s.keepDevDrive(context.Background())
	s.keepDevDrive(context.Background())

	// Assert
	if items := devDriveItems(s); starts != 1 || len(items) != 1 {
		t.Errorf("starts = %d, items = %+v; want neither again for this boot", starts, items)
	}

	// Act: it ran, D: is back and the task is this build's.
	machine.Volumes = append(machine.Volumes, devdrive.Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted})
	s.Options.DevDrive.TaskCurrent = func(context.Context) (bool, error) { return true, nil }
	s.keepDevDrive(context.Background())

	// Assert
	if items := devDriveItems(s); len(items) != 1 {
		t.Errorf("items = %+v, want none past the attach item", items)
	}
}

// The startup task the board started is waited for at most AttachBound: a
// task that never wrote its result does not hold the attach item back.
func TestTheStartupTaskIsWaitedForWithinItsBound(t *testing.T) {
	// Arrange
	s, _, _ := devDriveMissingService(t, 10*time.Minute, time.Hour, func(context.Context) error { return nil })
	s.keepDevDrive(context.Background())
	s.keepDevDrive(context.Background())
	if items := devDriveItems(s); len(items) != 0 {
		t.Fatalf("items = %+v while the task runs, want none", items)
	}

	// Act
	s.devDriveStarted = s.devDriveStarted.Add(-devdrive.AttachBound)
	s.keepDevDrive(context.Background())

	// Assert
	if items := devDriveItems(s); len(items) != 1 || items[0].DevDrive != "attach" {
		t.Errorf("items = %+v, want the attach item once the bound passed", items)
	}
}

// Attach on the panel asks again: the board starts the startup task first,
// before any item, whatever the boot.
func TestAttachOnThePanelStartsTheTaskFirst(t *testing.T) {
	// Arrange
	var starts int
	s, _, _ := devDriveMissingService(t, time.Hour, -time.Minute, func(context.Context) error { starts++; return nil })
	s.keepDevDrive(context.Background())
	if starts != 0 || len(devDriveItems(s)) != 0 {
		t.Fatalf("starts = %d, items = %+v before the ask; want none, the item came this boot", starts, devDriveItems(s))
	}

	// Act
	if err := s.askDevDrive(true); err != nil {
		t.Fatal(err)
	}
	s.keepDevDrive(context.Background())

	// Assert
	if starts != 1 || len(devDriveItems(s)) != 0 {
		t.Errorf("starts = %d, items = %+v; want the task started and no item yet", starts, devDriveItems(s))
	}
}

// An attached drive whose startup task is this build's offers nothing and
// starts nothing.
func TestAnAttachedDriveOffersNothing(t *testing.T) {
	// Arrange
	var starts int
	s, machine, _ := devDriveMissingService(t, time.Minute, time.Hour, func(context.Context) error { starts++; return nil })
	machine.Volumes = append(machine.Volumes, devdrive.Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted})
	s.Options.DevDrive.TaskCurrent = func(context.Context) (bool, error) { return true, nil }

	// Act
	s.keepDevDrive(context.Background())

	// Assert
	if items := devDriveItems(s); starts != 0 || len(items) != 0 {
		t.Errorf("starts = %d, items = %+v; want nothing", starts, items)
	}
	if view := s.devDriveViewNow(); view.State != "on" || view.Action != "" || view.Waiting != "" {
		t.Errorf("view = %+v, want on with nothing to press", view)
	}
}

// An install from before this build has the old startup task: while its
// drive is attached it gets one renew item, which registers the task this
// build makes, so the next restart needs no click. Left unrun, it is never
// offered again.
func TestAnOldStartupTaskGetsOneRenewItemOnce(t *testing.T) {
	// Arrange
	var reads int
	s, machine, vhd := devDriveMissingService(t, time.Hour, time.Hour, func(context.Context) error { return nil })
	machine.Volumes = append(machine.Volumes, devdrive.Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted})
	s.Options.DevDrive.TaskCurrent = func(context.Context) (bool, error) { reads++; return false, nil }

	// Act
	s.keepDevDrive(context.Background())

	// Assert
	items := devDriveItems(s)
	if len(items) != 1 || items[0].DevDrive != "task" || !items[0].Admin || items[0].Command != devdrive.CreateScript(vhd, "D:", 0) || !strings.Contains(items[0].Title, "Renew the startup task") {
		t.Fatalf("items = %+v, want one renew item that runs the create script", items)
	}

	// Act: it expires unrun, and the board looks again and again.
	endDevDriveItem(t, s, 1)
	for range 3 {
		s.keepDevDrive(context.Background())
	}

	// Assert
	if items := devDriveItems(s); len(items) != 1 || reads != 1 {
		t.Errorf("items = %+v after %d task reads; want the renew item offered once", items, reads)
	}
}

// A drive that goes missing while the renew item waits, as after a restart
// before the person ran it, is attached by that item: the line names it, and
// the board neither starts the old task nor offers an attach item beside
// it. Once it ended unrun, the board looks after the drive as after a boot.
func TestAMissingDriveWhileTheRenewItemWaitsNamesThatItem(t *testing.T) {
	// Arrange
	var starts int
	s, machine, _ := devDriveMissingService(t, time.Hour, time.Hour, func(context.Context) error { starts++; return nil })
	machine.Volumes = append(machine.Volumes, devdrive.Volume{Root: `D:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted})
	s.Options.DevDrive.TaskCurrent = func(context.Context) (bool, error) { return false, nil }
	s.keepDevDrive(context.Background())
	if items := devDriveItems(s); len(items) != 1 || items[0].DevDrive != "task" {
		t.Fatalf("items = %+v, want the renew item", items)
	}

	// Act: D: goes missing while the renew item waits.
	machine.Volumes = machine.Volumes[:1]
	s.keepDevDrive(context.Background())

	// Assert
	items := devDriveItems(s)
	view := s.devDriveViewNow()
	if starts != 0 || len(items) != 1 || items[0].DevDrive != "task" || view.Waiting != "task" || view.Action != "" || view.Line != devdrive.MissingLine(`D:\CodeGoblins`, devdrive.AttachByRenew) {
		t.Fatalf("starts = %d, items = %+v, view = %+v; want only the renew item, named by the line", starts, items, view)
	}
	if strings.Contains(items[0].Title, "stays attached") || !strings.Contains(items[0].Title, "if it is not attached") {
		t.Errorf("the renew item's title = %q, want it true of a missing drive", items[0].Title)
	}

	// Act: it ends unrun.
	endDevDriveItem(t, s, 1)
	s.keepDevDrive(context.Background())

	// Assert
	if view := s.devDriveViewNow(); starts != 1 || len(devDriveItems(s)) != 1 || view.Line != devdrive.MissingLine(`D:\CodeGoblins`, devdrive.AttachByTask) {
		t.Errorf("starts = %d, items = %+v, view = %+v; want the startup task started and no new item yet", starts, devDriveItems(s), view)
	}
}

// The line under the Dev Drive names the attach item only while one waits:
// once it ended, the line names the panel's Attach, which is there to press.
func TestTheLineNeverNamesAnItemThatDoesNotExist(t *testing.T) {
	// Arrange
	s, _, _ := devDriveMissingService(t, time.Minute, time.Hour, nil)
	s.keepDevDrive(context.Background())
	if view := s.devDriveViewNow(); view.Waiting != "attach" || !strings.Contains(view.Line, "Attach item waiting") {
		t.Fatalf("view = %+v, want the waiting item named", view)
	}

	// Act
	endDevDriveItem(t, s, 1)
	s.keepDevDrive(context.Background())

	// Assert
	view := s.devDriveViewNow()
	if view.Waiting != "" || view.Action != devDriveAttach || strings.Contains(view.Line, "item") || view.Line != devdrive.MissingLine(`D:\CodeGoblins`, devdrive.AttachByPanel) {
		t.Errorf("view = %+v, want the panel's Attach named and no item", view)
	}
}
