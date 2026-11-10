//go:build windows

package gatehome

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// symbolicLinkAllowUnprivileged lets an account in developer mode make a
// symbolic link without the privilege, as os.Symlink does.
const symbolicLinkAllowUnprivileged = 0x2

// linkKind says whether the link at path is a junction rather than a
// symbolic link, and whether it is a folder's link.
func linkKind(path string) (isJunction, isDirectory bool, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, false, err
	}
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false, false, fmt.Errorf("%s has no Windows attributes", path)
	}
	return info.Mode()&fs.ModeSymlink == 0, attributes.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0, nil
}

// createLink makes a link at path that leads to target: a junction, or a
// symbolic link, a folder's when isDirectory.
func createLink(path, target string, isJunction, isDirectory bool) error {
	if isJunction {
		return createJunction(path, target)
	}
	flags := uint32(symbolicLinkAllowUnprivileged)
	if isDirectory {
		flags |= windows.SYMBOLIC_LINK_FLAG_DIRECTORY
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	leadsTo, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.CreateSymbolicLink(name, leadsTo, flags)
}

// createJunction makes an empty folder at path and makes it a junction to
// the absolute folder target.
func createJunction(path, target string) error {
	substitute := `\??\` + target
	if rest, ok := strings.CutPrefix(target, `\\?\`); ok {
		substitute = `\??\` + rest
	}
	substituteName, printName := utf16.Encode([]rune(substitute)), utf16.Encode([]rune(target))
	names := append(append(append(substituteName, 0), printName...), 0)
	data := make([]byte, 16+2*len(names))
	binary.LittleEndian.PutUint32(data[0:], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(data[4:], uint16(8+2*len(names)))
	binary.LittleEndian.PutUint16(data[10:], uint16(2*len(substituteName)))
	binary.LittleEndian.PutUint16(data[12:], uint16(2*(len(substituteName)+1)))
	binary.LittleEndian.PutUint16(data[14:], uint16(2*len(printName)))
	for index, unit := range names {
		binary.LittleEndian.PutUint16(data[16+2*index:], unit)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return errors.Join(err, os.Remove(path))
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return errors.Join(err, os.Remove(path))
	}
	var returned uint32
	err = windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &data[0], uint32(len(data)), nil, 0, &returned, nil)
	err = errors.Join(err, windows.CloseHandle(handle))
	if err != nil {
		return errors.Join(err, os.Remove(path))
	}
	return nil
}
