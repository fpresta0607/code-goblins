package devdrive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

var (
	getCompressedFileSize           = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCompressedFileSizeW")
	getStorageDependencyInformation = windows.NewLazySystemDLL("virtdisk.dll").NewProc("GetStorageDependencyInformation")
)

// Read reads this machine without administrator rights: the Windows build
// from the registry, Defender's real-time protection from Get-MpComputerStatus,
// every fixed drive's file system, Dev Drive flag and trust from the drive
// itself, and the file each Dev Drive lives in, with what that file takes on
// its drive where this process may read it.
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
		if v.Dev {
			v.Image = readImage(v.Letter())
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

// readImage reads the file the drive letter lives in and what that file
// takes on its drive, the zero Image for a drive on a disk of its own.
func readImage(letter string) Image {
	path, err := imagePath(letter)
	switch {
	case err != nil:
		return Image{Unread: strings.TrimSuffix(err.Error(), ".")}
	case path == "":
		return Image{}
	}
	return sizeOf(path)
}

// The storage dependency query for the volumes a virtual disk lives on, from
// virtdisk.h, and the size GetCompressedFileSizeW answers when it fails.
const (
	storageDependencyInfoVersion2   = 2
	getStorageDependencyHostVolumes = 0x1
	invalidFileSize                 = 0xFFFFFFFF
)

// storageDependencyInfoType2 is STORAGE_DEPENDENCY_INFO_TYPE_2.
type storageDependencyInfoType2 struct {
	DependencyTypeFlags         uint32
	ProviderSpecificFlags       uint32
	VirtualStorageDeviceID      uint32
	VirtualStorageVendorID      windows.GUID
	AncestorLevel               uint32
	DependencyDeviceName        *uint16
	HostVolumeName              *uint16
	DependentVolumeName         *uint16
	DependentVolumeRelativePath *uint16
}

// imagePath asks Windows which file the drive letter lives in, through the
// storage dependency of its volume, which a handle that may neither read nor
// write the volume gets without administrator rights. It is "" for a drive on
// a disk of its own.
func imagePath(letter string) (string, error) {
	name, err := windows.UTF16PtrFromString(`\\.\` + letter)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]byte, 1024)
	for {
		*(*uint32)(unsafe.Pointer(&buffer[0])) = storageDependencyInfoVersion2
		var used uint32
		result, _, _ := getStorageDependencyInformation.Call(uintptr(handle), getStorageDependencyHostVolumes, uintptr(len(buffer)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&used)))
		errno := windows.Errno(result)
		if errno == windows.ERROR_INSUFFICIENT_BUFFER && int(used) > len(buffer) {
			buffer = make([]byte, used)
			continue
		}
		if errno == windows.ERROR_VIRTDISK_NOT_VIRTUAL_DISK {
			return "", nil
		}
		if errno != 0 {
			return "", errno
		}
		break
	}
	if *(*uint32)(unsafe.Pointer(&buffer[4])) == 0 {
		return "", nil
	}
	dependency := (*storageDependencyInfoType2)(unsafe.Pointer(&buffer[8]))
	host := windows.UTF16PtrToString(dependency.HostVolumeName) + `\`
	relative := windows.UTF16PtrToString(dependency.DependentVolumeRelativePath)
	hostName, err := windows.UTF16PtrFromString(host)
	if err != nil {
		return "", err
	}
	var mounts [windows.MAX_PATH + 1]uint16
	var length uint32
	if err := windows.GetVolumePathNamesForVolumeName(hostName, &mounts[0], uint32(len(mounts)), &length); err != nil {
		return "", err
	}
	if mount := windows.UTF16ToString(mounts[:]); mount != "" {
		host = mount
	}
	return filepath.Join(host, relative), nil
}

// sizeOf reads what the file at path takes on its drive, which for a sparse
// or compressed file is less than its length. Windows tells it only to a
// process that may open the file, and the folder of the Dev Drive Code
// Goblins made is open to administrators and Windows alone.
func sizeOf(path string) Image {
	image := Image{Path: path}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		image.Unread = err.Error()
		return image
	}
	var high uint32
	low, _, lastErr := getCompressedFileSize.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&high)))
	errno, _ := lastErr.(windows.Errno)
	switch {
	case uint32(low) != invalidFileSize || errno == 0:
		image.Size = uint64(high)<<32 | uint64(uint32(low))
	case errno == windows.ERROR_ACCESS_DENIED && !windows.GetCurrentProcessToken().IsElevated():
		image.Unread = "only a process run as administrator may read it"
	default:
		image.Unread = strings.TrimSuffix(errno.Error(), ".")
	}
	return image
}
