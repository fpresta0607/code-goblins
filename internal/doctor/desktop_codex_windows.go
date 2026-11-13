package doctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// codexPackageFamily is the Codex desktop app's Windows package family, its
// name and publisher, which every version of the app keeps.
const codexPackageFamily = "OpenAI.Codex_2p2nqsd0c76g0"

var (
	packagesByFamily  = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetPackagesByPackageFamily")
	packagePathByName = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetPackagePathByFullName")
)

// DesktopCodex is the codex.exe the Codex desktop app bundles in its
// app\resources folder, found by the app's package family for this user, and
// whether the app is installed at all. Its folder under WindowsApps cannot be
// listed, and its name changes with every version of the app.
func DesktopCodex() (string, bool, error) {
	family, err := windows.UTF16PtrFromString(codexPackageFamily)
	if err != nil {
		return "", false, err
	}
	var count, length uint32
	result, _, _ := packagesByFamily.Call(uintptr(unsafe.Pointer(family)), uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&length)), 0)
	if windows.Errno(result) != windows.ERROR_INSUFFICIENT_BUFFER {
		if result != 0 {
			return "", false, fmt.Errorf("the Codex desktop app's packages could not be listed: %w", windows.Errno(result))
		}
		return "", false, nil
	}
	names := make([]*uint16, count)
	buffer := make([]uint16, length)
	result, _, _ = packagesByFamily.Call(uintptr(unsafe.Pointer(family)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&names[0])), uintptr(unsafe.Pointer(&length)), uintptr(unsafe.Pointer(&buffer[0])))
	if result != 0 {
		return "", false, fmt.Errorf("the Codex desktop app's packages could not be listed: %w", windows.Errno(result))
	}
	var problems error
	for _, name := range names[:count] {
		var size uint32
		result, _, _ := packagePathByName.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&size)), 0)
		if windows.Errno(result) != windows.ERROR_INSUFFICIENT_BUFFER {
			problems = errors.Join(problems, fmt.Errorf("the Codex desktop app's folder could not be read: %w", windows.Errno(result)))
			continue
		}
		path := make([]uint16, size)
		result, _, _ = packagePathByName.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&path[0])))
		if result != 0 {
			problems = errors.Join(problems, fmt.Errorf("the Codex desktop app's folder could not be read: %w", windows.Errno(result)))
			continue
		}
		program := filepath.Join(windows.UTF16ToString(path), "app", "resources", "codex.exe")
		if info, err := os.Stat(program); err == nil && info.Mode().IsRegular() {
			return program, true, nil
		}
	}
	if problems != nil {
		return "", true, problems
	}
	return "", true, errors.New("the Codex desktop app is installed without a bundled codex.exe in app\\resources")
}
