package supervisor

import (
	"errors"
	"reflect"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/disk"
)

// AFK mode records free disk, as the board's disk meter reads it, when it
// turns on and when it turns off, and the report of the stretch sets the two
// beside each other for his page of the stretch. A disk that cannot be read at
// either end stops no switch and leaves the report without it.
func TestAFKModeRecordsFreeDiskWhenItTurnsOnAndWhenItTurnsOff(t *testing.T) {
	unreadable := errors.New("disk cannot be read")
	read := func(free uint64) func() (Disk, error) {
		return func() (Disk, error) { return diskWithFree(free * gigabyte), nil }
	}
	failed := func() (Disk, error) { return Disk{}, unreadable }
	reading := func(free uint64) *disk.Reading {
		return &disk.Reading{Drive: "C:", Free: free * gigabyte, Total: 500 * gigabyte}
	}
	tests := []struct {
		name          string
		on, off       func() (Disk, error)
		before, after *disk.Reading
		used          *afk.DiskUse
	}{
		{name: "read at both ends", on: read(340), off: read(337), before: reading(340), after: reading(337), used: &afk.DiskUse{Drive: "C:", Total: 500 * gigabyte, On: 340 * gigabyte, Off: 337 * gigabyte}},
		{name: "not read when it turned on", on: failed, off: read(337), after: reading(337)},
		{name: "not read when it turned off", on: read(340), off: failed, before: reading(340)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			s := boardService(store)
			asOverlordsBoard(s)
			s.Options.Dispatch = &Dispatch{Disk: test.on}

			// Act
			onCode, onBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":true}`, nil)
			switched, readErr := afk.Read(h.State)
			s.Options.Dispatch.Disk = test.off
			offCode, offBody := askTheBoard(t, s, "POST", "/api/afk", `{"on":false}`, nil)

			// Assert
			if onCode != 200 || offCode != 200 || readErr != nil {
				t.Fatalf("POST on = %d %s, off = %d %s, and the switch read %v, want each switch made", onCode, onBody, offCode, offBody, readErr)
			}
			if !switched.On || !reflect.DeepEqual(switched.Disk, test.before) {
				t.Errorf("the switch while on = %+v with free disk %+v, want %+v kept from when it turned on", switched, switched.Disk, test.before)
			}
			report, found, err := afk.ReadReport(h.State)
			if err != nil || !found {
				t.Fatalf("ReadReport = %v, %v, want the report of the stretch", found, err)
			}
			if !reflect.DeepEqual(report.DiskBefore, test.before) || !reflect.DeepEqual(report.DiskAfter, test.after) {
				t.Errorf("the report's free disk = %+v then %+v, want %+v then %+v", report.DiskBefore, report.DiskAfter, test.before, test.after)
			}
			if got := afk.DiskUsed(report.DiskBefore, report.DiskAfter); !reflect.DeepEqual(got, test.used) {
				t.Errorf("the report's free disk used = %+v, want %+v", got, test.used)
			}
		})
	}
}
