package conpty

import (
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows' inbox conhost enqueues a program's waiting console read outside
// the console lock, so input written at that moment can find the wait half
// made: the key is stranded until more input arrives, or conhost crashes
// reading a null pointer in ConsoleWaitBlock's destructor and the terminal
// ends. Microsoft fixed that in microsoft/terminal#18816, which Windows does
// not ship yet but its ConPTY package does. So consoles run on that
// package's conpty.dll and OpenConsole.exe, embedded here, and on the system
// conhost only when those cannot be written or loaded.

// openConsoleVersion is the Microsoft.Windows.Console.ConPTY package the
// embedded files come from, as tools/notices lists it.
var openConsoleVersion = strings.TrimSpace(embeddedVersion)

//go:embed openconsole/VERSION
var embeddedVersion string

//go:embed openconsole/conpty.dll
var embeddedConptyDLL []byte

//go:embed openconsole/OpenConsole.exe
var embeddedOpenConsole []byte

// consoleHost is the pseudo console API consoles are made with, the
// console server program it starts for each, and whether that server asks
// its terminal for its device attributes as it starts.
type consoleHost struct {
	create               func(size windows.Coord, input, output windows.Handle, flags uint32, console *windows.Handle) error
	resize               func(console windows.Handle, size windows.Coord) error
	close                func(console windows.Handle)
	server               string
	asksDeviceAttributes bool
}

var (
	chooseHost sync.Once
	chosenHost consoleHost
	// hostProblem is why consoles run on the system conhost, nil when they
	// run on the embedded OpenConsole.
	hostProblem error
	// heldFiles keeps the embedded files open, refusing writes and deletes,
	// for as long as this process may start a console from them.
	heldFiles []*os.File
)

// currentConsoleHost is the console host every console in this process runs
// on, chosen once.
func currentConsoleHost() consoleHost {
	chooseHost.Do(func() {
		var err error
		if chosenHost, err = loadOpenConsole(); err != nil {
			hostProblem = fmt.Errorf("the embedded OpenConsole %s cannot be used, so terminals run on the system conhost: %w", openConsoleVersion, err)
			chosenHost = systemConsoleHost()
		}
	})
	return chosenHost
}

// ConsoleHostProblem says why this process's consoles run on the system
// conhost rather than the embedded OpenConsole, or nil when they do not.
func ConsoleHostProblem() error {
	currentConsoleHost()
	return hostProblem
}

func systemConsoleHost() consoleHost {
	system, _ := windows.GetSystemDirectory()
	return consoleHost{create: windows.CreatePseudoConsole, resize: windows.ResizePseudoConsole, close: windows.ClosePseudoConsole, server: filepath.Join(system, "conhost.exe")}
}

// loadOpenConsole places the embedded files in a folder only this user can
// write, checks each against the embedded copy through a handle it keeps,
// and loads conpty.dll, which starts the OpenConsole.exe beside it.
func loadOpenConsole() (consoleHost, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return consoleHost{}, err
	}
	dir := filepath.Join(cache, "cfo", "conpty", openConsoleVersion)
	if err := userOnlyFolder(dir); err != nil {
		return consoleHost{}, err
	}
	dllPath, server := filepath.Join(dir, "conpty.dll"), filepath.Join(dir, "OpenConsole.exe")
	for path, content := range map[string][]byte{dllPath: embeddedConptyDLL, server: embeddedOpenConsole} {
		file, err := placeVerified(path, content)
		if err != nil {
			return consoleHost{}, err
		}
		heldFiles = append(heldFiles, file)
	}
	// conpty.dll needs nothing but Windows' own libraries.
	dll, err := windows.LoadLibraryEx(dllPath, 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return consoleHost{}, fmt.Errorf("load %s: %w", dllPath, err)
	}
	var procs [3]uintptr
	for i, name := range []string{"CreatePseudoConsole", "ResizePseudoConsole", "ClosePseudoConsole"} {
		if procs[i], err = windows.GetProcAddress(dll, name); err != nil {
			return consoleHost{}, fmt.Errorf("find %s in %s: %w", name, dllPath, err)
		}
	}
	coord := func(size windows.Coord) uintptr { return uintptr(*(*uint32)(unsafe.Pointer(&size))) }
	return consoleHost{
		create: func(size windows.Coord, input, output windows.Handle, flags uint32, console *windows.Handle) error {
			if hresult, _, _ := syscall.SyscallN(procs[0], coord(size), uintptr(input), uintptr(output), uintptr(flags), uintptr(unsafe.Pointer(console))); hresult != 0 {
				return windows.Errno(hresult)
			}
			return nil
		},
		resize: func(console windows.Handle, size windows.Coord) error {
			if hresult, _, _ := syscall.SyscallN(procs[1], uintptr(console), coord(size)); hresult != 0 {
				return windows.Errno(hresult)
			}
			return nil
		},
		close:                func(console windows.Handle) { syscall.SyscallN(procs[2], uintptr(console)) },
		server:               server,
		asksDeviceAttributes: true,
	}, nil
}

// userOnlyFolder makes dir and replaces its access list with one that lets
// this Windows user, and no one else, in, whoever made the folder.
func userOnlyFolder(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read this user's identity: %w", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("make %s this user's alone: %w", dir, err)
	}
	return nil
}

// placeVerified returns path open, refusing writes and deletes, once its
// SHA-256 is content's, writing content there when it is missing or differs.
// What was checked is then what Windows loads.
func placeVerified(path string, content []byte) (*os.File, error) {
	want := sha256.Sum256(content)
	var written error
	for range 2 {
		file, err := holdFile(path)
		if err == nil {
			hash := sha256.New()
			if _, err := io.Copy(hash, file); err != nil {
				file.Close()
				return nil, err
			}
			if [sha256.Size]byte(hash.Sum(nil)) == want {
				return file, nil
			}
			file.Close()
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		// Another process holding a file here checked it first, so a
		// replacement it refuses is followed by holding that file.
		written = writeBeside(path, content)
		if written != nil && !errors.Is(written, windows.ERROR_SHARING_VIOLATION) && !errors.Is(written, windows.ERROR_ACCESS_DENIED) {
			return nil, written
		}
	}
	return nil, errors.Join(fmt.Errorf("%s does not hold the embedded copy", path), written)
}

// holdFile opens path for reading and refuses every other open that would
// write, rename or delete it while this handle lives.
func holdFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// writeBeside writes content to a new file in path's folder and renames it
// over path.
func writeBeside(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		os.Remove(file.Name())
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		os.Remove(file.Name())
		return err
	}
	return nil
}
