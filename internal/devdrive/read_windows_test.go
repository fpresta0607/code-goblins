package devdrive

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Read works without administrator rights, which `cfo doctor` and the board
// have: it names this Windows, the system drive and its file system, and never
// takes a drive that is not ReFS for a Dev Drive.
func TestReadReadsThisMachineWithoutAdministratorRights(t *testing.T) {
	m, err := Read(context.Background(), execx.OSRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Build < 10240 || m.SystemFree == 0 || m.SystemDrive == "" {
		t.Errorf("machine = %+v, want this Windows' build and the system drive's free space", m)
	}
	system, found := m.VolumeOf(os.Getenv("SystemDrive") + `\Windows`)
	if !found || system.FileSystem == "" || system.Total == 0 {
		t.Errorf("the system drive is not among %+v", m.Volumes)
	}
	for _, v := range m.Volumes {
		if v.Dev && !strings.EqualFold(v.FileSystem, "ReFS") {
			t.Errorf("%s is %s and was taken for a Dev Drive", v.Root, v.FileSystem)
		}
	}
	if m.Defender && m.DefenderUnread != "" {
		t.Errorf("Defender read as on with a reason it could not be read: %q", m.DefenderUnread)
	}
	for _, v := range m.Volumes {
		switch {
		case !v.Dev && v.Image != Image{}:
			t.Errorf("%s is no Dev Drive and its file was read: %+v", v.Root, v.Image)
		case v.Image.Path != "" && (!filepath.IsAbs(v.Image.Path) || strings.EqualFold(filepath.VolumeName(v.Image.Path), v.Letter())):
			t.Errorf("%s lives in %q, which is not a file on another drive", v.Root, v.Image.Path)
		case v.Image.Path != "" && v.Image.Size == 0 && v.Image.Unread == "":
			t.Errorf("%s lives in %q, whose size was neither read nor said unread", v.Root, v.Image.Path)
		}
	}
}

// sizeOf reads what a file takes on its drive, not its length: a sparse file
// as long as a dynamically expanding disk file can grow takes only what was
// written into it.
func TestSizeOfReadsWhatAFileTakesOnItsDrive(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "sparse.vhdx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	var returned uint32
	if err := windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &returned, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte{0xA5}, 1<<20)); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	// ReFS, which a Dev Drive is, allocates written data once it reaches the
	// disk.
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Act
	image := sizeOf(path)

	// Assert
	if image.Path != path || image.Unread != "" || image.Size < 1<<20 || image.Size >= 2<<20 {
		t.Errorf("sizeOf = %+v, want the 1 MB written into the 64 MB file", image)
	}
}

// The folder of the Dev Drive Code Goblins made is open to administrators and
// Windows alone, as CreateScript's icacls leaves it: a process run as
// administrator reads the file's size, and any other is told only one run as
// administrator may.
func TestSizeOfAFileOnlyAdministratorsMayOpenSaysWhoMay(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	path := filepath.Join(folder, "CodeGoblins.vhdx")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0xA5}, 3<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := windows.GetNamedSecurityInfo(folder, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	restore, _, err := before.DACL()
	if err != nil {
		t.Fatal(err)
	}
	locked, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;BA)(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := locked.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(folder, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.SetNamedSecurityInfo(folder, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, restore, nil); err != nil {
			t.Error(err)
		}
	})

	// Act
	image := sizeOf(path)

	// Assert
	if windows.GetCurrentProcessToken().IsElevated() {
		if image.Size != 3<<20 || image.Unread != "" {
			t.Errorf("sizeOf as administrator = %+v, want the 3 MB file read", image)
		}
		return
	}
	if image.Path != path || image.Size != 0 || image.Unread != "only a process run as administrator may read it" {
		t.Errorf("sizeOf = %+v, want it unread because only a process run as administrator may read it", image)
	}
}
