package devdrive

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Read reads this machine without administrator rights: the Windows build
// from the registry, Defender's real-time protection from Get-MpComputerStatus,
// and every fixed drive's file system, Dev Drive flag and trust from the drive
// itself.
func Read(ctx context.Context, commands execx.Runner) (Machine, error) {
	var m Machine
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return Machine{}, err
	}
	defer key.Close()
	build, _, err := key.GetStringValue("CurrentBuildNumber")
	if err != nil {
		return Machine{}, err
	}
	if m.Build, err = strconv.Atoi(build); err != nil {
		return Machine{}, err
	}
	revision, _, err := key.GetIntegerValue("UBR")
	if err != nil {
		return Machine{}, err
	}
	m.Revision = int(revision)
	m.Defender, m.DefenderUnread = defenderRealTime(ctx, commands)
	m.SystemDrive = os.Getenv("SystemDrive")
	if m.SystemDrive == "" {
		m.SystemDrive = "C:"
	}
	reading, err := disk.Read(m.SystemDrive + `\`)
	if err != nil {
		return Machine{}, err
	}
	m.SystemFree = reading.Free
	m.Volumes, m.Letters, err = volumes()
	return m, err
}

// defenderRealTime asks Defender whether its real-time protection is on, the
// one reading of it that holds under tamper protection, a policy or another
// antivirus that put Defender in passive mode.
func defenderRealTime(ctx context.Context, commands execx.Runner) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := commands.Run(ctx, execx.Request{Name: "powershell.exe", Args: []string{"-NoProfile", "-NonInteractive", "-Command", "(Get-MpComputerStatus).RealTimeProtectionEnabled"}})
	switch {
	case err != nil:
		return false, err.Error()
	case result.ExitCode != 0:
		return false, "Get-MpComputerStatus: " + strings.TrimSpace(string(result.Stderr))
	}
	switch strings.TrimSpace(string(result.Stdout)) {
	case "True":
		return true, ""
	case "False":
		return false, ""
	}
	return false, "Get-MpComputerStatus answered " + strconv.Quote(strings.TrimSpace(string(result.Stdout)))
}

// volumes lists every fixed drive with a letter, and every letter in use.
func volumes() ([]Volume, uint32, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, 0, err
	}
	var found []Volume
	for index := range 26 {
		if mask&(1<<index) == 0 {
			continue
		}
		root := string(rune('A'+index)) + `:\`
		rootName, err := windows.UTF16PtrFromString(root)
		if err != nil {
			return nil, 0, err
		}
		if windows.GetDriveType(rootName) != windows.DRIVE_FIXED {
			continue
		}
		var label, fileSystem [windows.MAX_PATH + 1]uint16
		if err := windows.GetVolumeInformation(rootName, &label[0], uint32(len(label)), nil, nil, nil, &fileSystem[0], uint32(len(fileSystem))); err != nil {
			continue
		}
		v := Volume{Root: root, Label: windows.UTF16ToString(label[:]), FileSystem: windows.UTF16ToString(fileSystem[:])}
		if reading, err := disk.Read(root); err == nil {
			v.Free, v.Total = reading.Free, reading.Total
		}
		if strings.EqualFold(v.FileSystem, "ReFS") {
			v.Dev, v.Trust = persistentState(root)
		}
		found = append(found, v)
	}
	return found, mask, nil
}

// The persistent volume state a Dev Drive carries, from winioctl.h.
const (
	fsctlQueryPersistentVolumeState = 0x9023c
	persistentVolumeDev             = 0x2000
	persistentVolumeTrusted         = 0x4000
)

// persistentVolumeInformation is FILE_FS_PERSISTENT_VOLUME_INFORMATION.
type persistentVolumeInformation struct {
	VolumeFlags, FlagMask, Version, Reserved uint32
}

// persistentState asks the drive at root whether it is a Dev Drive and
// whether it is trusted, through a handle to its root folder, which needs no
// administrator rights. Each flag is asked on its own, because a file system
// refuses a mask with a flag it does not know: NTFS refuses both, and a Dev
// Drive that refused the trust flag would otherwise lose its Dev flag too.
func persistentState(root string) (bool, Trust) {
	dev, err := persistentFlag(root, persistentVolumeDev)
	if err != nil || !dev {
		return false, ""
	}
	trusted, err := persistentFlag(root, persistentVolumeTrusted)
	switch {
	case err != nil:
		return true, TrustUnknown
	case trusted:
		return true, Trusted
	}
	return true, Untrusted
}

func persistentFlag(root string, flag uint32) (bool, error) {
	name, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return false, err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(handle)
	in := persistentVolumeInformation{FlagMask: flag, Version: 1}
	var out persistentVolumeInformation
	var returned uint32
	if err := windows.DeviceIoControl(handle, fsctlQueryPersistentVolumeState, (*byte)(unsafe.Pointer(&in)), uint32(unsafe.Sizeof(in)), (*byte)(unsafe.Pointer(&out)), uint32(unsafe.Sizeof(out)), &returned, nil); err != nil {
		return false, err
	}
	if returned < uint32(unsafe.Sizeof(out)) {
		return false, errors.New("devdrive: the drive answered with less than its volume state")
	}
	return out.VolumeFlags&flag != 0, nil
}
