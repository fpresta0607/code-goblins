package disk

import (
	"errors"
	"slices"
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

func TestReadDrivesShowsEachDriveTheFleetWritesToOnce(t *testing.T) {
	// Arrange: the home on C:, a Dev Drive on D:, and a gate home the test
	// moves between them; E: is a detached drive and C:\no-gate-yet a gate
	// home not made yet.
	spaces := map[string][2]uint64{
		`C:\home`:         {300 << 30, 900 << 30},
		`C:\Users\me\.nm`: {300 << 30, 900 << 30},
		`D:\CodeGoblins`:  {80 << 30, 200 << 30},
		`d:\gate`:         {80 << 30, 200 << 30},
	}
	prior := spaceOf
	t.Cleanup(func() { spaceOf = prior })
	spaceOf = func(path string) (uint64, uint64, error) {
		values, ok := spaces[path]
		if !ok {
			return 0, 0, errors.New("the system cannot find the path specified")
		}
		return values[0], values[1], nil
	}
	home := Folder{Path: `C:\home`}
	devDrive := Folder{Path: `D:\CodeGoblins`, IsDevDrive: true}
	cDrive := Drive{Reading: Reading{Drive: "C:", Free: 300 << 30, Total: 900 << 30}}
	dDrive := Drive{Reading: Reading{Drive: "D:", Free: 80 << 30, Total: 200 << 30}, IsDevDrive: true}
	cases := []struct {
		name    string
		folders []Folder
		want    []Drive
	}{
		{"one drive", []Folder{home, {}, {Path: `C:\Users\me\.nm`, IsOptional: true}}, []Drive{cDrive}},
		{"two drives", []Folder{home, devDrive, {Path: `C:\Users\me\.nm`, IsOptional: true}}, []Drive{cDrive, dDrive}},
		{"a gate home sharing the Dev Drive", []Folder{home, devDrive, {Path: `d:\gate`, IsOptional: true}}, []Drive{cDrive, dDrive}},
		{"a gate home not made yet on a readable drive", []Folder{{Path: `C:\no-gate-yet`, IsOptional: true}, home}, []Drive{cDrive}},
		{"an unreadable Dev Drive folder beside a readable gate home", []Folder{home, {Path: `D:\gone`, IsDevDrive: true}, {Path: `d:\gate`, IsOptional: true}}, []Drive{cDrive, {Reading: Reading{Drive: "D:"}, IsDevDrive: true, Error: "the system cannot find the path specified"}}},
		{"a readable gate home before an unreadable Dev Drive folder", []Folder{home, {Path: `d:\gate`, IsOptional: true}, {Path: `D:\gone`, IsDevDrive: true}}, []Drive{cDrive, {Reading: Reading{Drive: "d:"}, IsDevDrive: true, Error: "the system cannot find the path specified"}}},
		{"an unreadable Dev Drive folder on the home's drive", []Folder{home, {Path: `C:\gone`, IsDevDrive: true}, {Path: `C:\Users\me\.nm`, IsOptional: true}}, []Drive{{Reading: Reading{Drive: "C:"}, IsDevDrive: true, Error: "the system cannot find the path specified"}}},
		{"an unreadable home beside a readable gate home on its drive", []Folder{{Path: `C:\gone`}, devDrive, {Path: `C:\Users\me\.nm`, IsOptional: true}}, []Drive{{Reading: Reading{Drive: "C:"}, Error: "the system cannot find the path specified"}, dDrive}},
		{"an unreadable drive", []Folder{home, {Path: `E:\detached`, IsDevDrive: true}}, []Drive{cDrive, {Reading: Reading{Drive: "E:"}, IsDevDrive: true, Error: "the system cannot find the path specified"}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Act
			drives := ReadDrives(test.folders...)

			// Assert
			if !slices.Equal(drives, test.want) {
				t.Errorf("ReadDrives = %+v, want %+v", drives, test.want)
			}
		})
	}
}
