package access

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// The accounts every narrowed folder admits beside the one the fleet runs
// as, by their well-known SIDs.
const (
	systemSID         = "S-1-5-18"
	administratorsSID = "S-1-5-32-544"
)

// fileAllAccess is FILE_ALL_ACCESS, the full control an access list's FA
// stands for.
const fileAllAccess = 0x1F01FF

// TreeSetNamedSecurityInfoW's TREE_SEC_INFO_SET, which hands the new list on
// to what the object holds by each entry's own inheritance, and the
// PROG_INVOKE_SETTING ProgressInvokeOnError, which calls back only for an
// object it could not change.
const (
	treeSecInfoSet        = 1
	progressInvokeOnError = 3
)

// shownUnchanged is how many of the objects a narrowing could not change its
// error names.
const shownUnchanged = 3

var treeSetNamedSecurityInfo = windows.NewLazySystemDLL("advapi32.dll").NewProc("TreeSetNamedSecurityInfoW")

// Windows keeps a callback for as long as the program runs and has room for
// a fixed number of them, so every walk shares one, which counts into
// unchanged while walking is held.
var (
	walking  sync.Mutex
	callback = sync.OnceValue(func() uintptr {
		return syscall.NewCallback(func(object *uint16, status uint32, _ *uint32, _ uintptr, _ int32) uintptr {
			unchanged.count++
			if len(unchanged.paths) < shownUnchanged {
				unchanged.paths = append(unchanged.paths, windows.UTF16PtrToString(object)+": "+syscall.Errno(status).Error())
			}
			return 0
		})
	})
	unchanged struct {
		count int
		paths []string
	}
)

// Narrow gives path, and everything in it that takes its access from its
// folder, to SYSTEM, Administrators and the account this process runs as
// alone, each with full control: its access list names no one else and
// inherits nothing from the folder above it. Windows walks everything in
// path to hand the list on, which takes minutes for a folder holding
// hundreds of thousands of files, and never follows a link to what it points
// at. A file or folder whose list cannot be changed, such as one another
// account owns that does not let this one change its list, keeps its access
// and makes Narrow return an *Unchanged naming the first few; the rest is
// changed all the same, and UnfinishedMarker keeps the folder from reading
// as narrowed until a later Narrow changes everything.
func Narrow(path string) error {
	sids, err := narrowed()
	if err != nil {
		return err
	}
	if err := limit(path, sids); err != nil {
		var left *Unchanged
		if errors.As(err, &left) {
			if markErr := fsx.AtomicWriteFile(filepath.Join(path, UnfinishedMarker), []byte(left.Error()+"\r\n")); markErr != nil {
				return errors.Join(err, markErr)
			}
		}
		return err
	}
	if err := os.Remove(filepath.Join(path, UnfinishedMarker)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// IsNarrow reports whether path is narrowed already: its own access list
// admits only SYSTEM, Administrators and the account this process runs as,
// inheriting nothing, and no narrowing of it was left unfinished. It reads
// only path itself and that marker.
func IsNarrow(path string) (bool, error) {
	sids, err := narrowed()
	if err != nil {
		return false, err
	}
	if limited, err := isLimited(path, sids); err != nil || !limited {
		return false, err
	}
	_, err = os.Lstat(filepath.Join(path, UnfinishedMarker))
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	return false, err
}

// limit gives path and everything made in it to the accounts sids name
// alone, each with full control, reporting what it could not change.
func limit(path string, sids []string) error {
	dacl, err := fullControl(sids)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	walking.Lock()
	defer walking.Unlock()
	unchanged.count, unchanged.paths = 0, nil
	status, _, _ := treeSetNamedSecurityInfo.Call(uintptr(unsafe.Pointer(name)), uintptr(windows.SE_FILE_OBJECT), uintptr(windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION), 0, 0, uintptr(unsafe.Pointer(dacl)), 0, treeSecInfoSet, callback(), progressInvokeOnError, 0)
	if status != 0 {
		return fmt.Errorf("set the access list of %s: %w", path, syscall.Errno(status))
	}
	if unchanged.count > 0 {
		return &Unchanged{Count: unchanged.count, Examples: unchanged.paths}
	}
	return nil
}

// narrowed are the SIDs a narrowed folder admits, sorted and each once, as a
// process that runs as SYSTEM is SYSTEM.
func narrowed() ([]string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return slices.Compact(slices.Sorted(slices.Values([]string{systemSID, administratorsSID, user.User.Sid.String()}))), nil
}

// fullControl is the protected access list that allows each of sids full
// control of a folder and everything made in it.
func fullControl(sids []string) (*windows.ACL, error) {
	var sddl strings.Builder
	sddl.WriteString("D:P")
	for _, sid := range sids {
		sddl.WriteString("(A;OICI;FA;;;" + sid + ")")
	}
	descriptor, err := windows.SecurityDescriptorFromString(sddl.String())
	if err != nil {
		return nil, err
	}
	dacl, _, err := descriptor.DACL()
	return dacl, err
}

// isLimited reports whether path's access list takes nothing from its
// folder and allows sids, sorted, and no one else, full control of it and of
// everything made in it.
func isLimited(path string, sids []string) (bool, error) {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false, err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil || int(dacl.AceCount) != len(sids) {
		return false, err
	}
	var allowed []string
	for index := range uint32(dacl.AceCount) {
		var entry *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &entry); err != nil {
			return false, err
		}
		if entry.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || entry.Header.AceFlags != windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE || entry.Mask != fileAllAccess {
			return false, nil
		}
		allowed = append(allowed, (*windows.SID)(unsafe.Pointer(&entry.SidStart)).String())
	}
	slices.Sort(allowed)
	return slices.Equal(allowed, sids), nil
}
