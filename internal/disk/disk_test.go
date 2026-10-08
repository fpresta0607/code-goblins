package disk

import (
	"errors"
	"testing"
)

func TestReadReadsTheDriveHoldingAFolder(t *testing.T) {
	reading, err := Read(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if reading.Total == 0 || reading.Free == 0 || reading.Free > reading.Total || reading.Drive == "" {
		t.Errorf("reading = %+v, want a named drive with some space of its size free", reading)
	}
}

func TestReadLeastNamesTheDriveWithTheLeastFreeSpace(t *testing.T) {
	// Arrange: the home's drive and a Dev Drive, the second fuller.
	spaces := map[string][2]uint64{`C:\home`: {300 << 30, 900 << 30}, `D:\CodeGoblins`: {8 << 30, 200 << 30}}
	prior := spaceOf
	t.Cleanup(func() { spaceOf = prior })
	spaceOf = func(path string) (uint64, uint64, error) {
		values, ok := spaces[path]
		if !ok {
			return 0, 0, errors.New("the system cannot find the path specified")
		}
		return values[0], values[1], nil
	}

	// Act
	least, err := ReadLeast(`C:\home`, "", `D:\CodeGoblins`)

	// Assert
	if err != nil || least.Drive != "D:" || least.Free != 8<<30 {
		t.Fatalf("ReadLeast = %+v, %v; want D: with 8 GB free", least, err)
	}
	if only, err := ReadLeast(`C:\home`, ""); err != nil || only.Drive != "C:" {
		t.Errorf("ReadLeast without a Dev Drive = %+v, %v; want the home's drive", only, err)
	}
	if _, err := ReadLeast(`C:\home`, `E:\detached`); err == nil {
		t.Error("a drive that cannot be read was skipped, so a detached Dev Drive would read as room to start")
	}
}
