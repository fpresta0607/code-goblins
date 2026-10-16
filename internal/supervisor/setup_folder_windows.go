package supervisor

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const FOLDER_DIALOG_CANCELLED = 0x800704c7

// IFileDialog and IShellItem use the COM vtable order in shobjidl_core.h.
type folderDialog struct{ methods *folderDialogMethods }
type folderDialogMethods struct {
	QueryInterface, AddRef, Release, Show                              uintptr
	SetFileTypes, SetFileTypeIndex, GetFileTypeIndex, Advise, Unadvise uintptr
	SetOptions, GetOptions, SetDefaultFolder, SetFolder, GetFolder     uintptr
	GetCurrentSelection, SetFileName, GetFileName, SetTitle            uintptr
	SetOKButtonLabel, SetFileNameLabel, GetResult                      uintptr
}
type folderShellItem struct{ methods *folderShellItemMethods }
type folderShellItemMethods struct {
	QueryInterface, AddRef, Release, BindToHandler, GetParent, GetDisplayName uintptr
}

var (
	folderUser32        = windows.NewLazySystemDLL("user32.dll")
	folderTimersMu      sync.Mutex
	folderTimers        = map[uintptr]*folderDialogWatch{}
	folderTimerCallback = sync.OnceValue(func() uintptr {
		return syscall.NewCallback(func(_ uintptr, _ uint32, timer uintptr, _ uint32) uintptr {
			folderTimersMu.Lock()
			watch := folderTimers[timer]
			folderTimersMu.Unlock()
			if watch == nil {
				return 0
			}
			if watch.window == 0 {
				watch.window, _, _ = folderUser32.NewProc("GetActiveWindow").Call()
			}
			if watch.window == 0 {
				return 0
			}
			if watch.ctx.Err() != nil {
				// Cancel on the dialog's own thread to end its modal loop.
				folderUser32.NewProc("PostMessageW").Call(watch.window, 0x111, 2, 0)
				return 0
			}
			if !watch.isRaised {
				// The click belongs to the board's UI thread, while the
				// dialog belongs to the supervisor's STA thread.
				thread := windows.GetCurrentThreadId()
				ownerThread, _ := windows.GetWindowThreadProcessId(watch.owner, nil)
				attached := uintptr(0)
				if ownerThread != 0 && ownerThread != thread {
					attached, _, _ = folderUser32.NewProc("AttachThreadInput").Call(uintptr(thread), uintptr(ownerThread), 1)
				}
				folderUser32.NewProc("SetForegroundWindow").Call(watch.window)
				if attached != 0 {
					folderUser32.NewProc("AttachThreadInput").Call(uintptr(thread), uintptr(ownerThread), 0)
				}
				watch.isRaised = true
			}
			return 0
		})
	})
)

type folderDialogWatch struct {
	ctx      context.Context
	window   uintptr
	owner    windows.HWND
	isRaised bool
}

func folderResult(operation string, result uintptr) error {
	if int32(result) < 0 {
		return fmt.Errorf("%s failed (HRESULT 0x%08x)", operation, uint32(result))
	}
	return nil
}

func pickProjectsFolder(ctx context.Context, root string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Show pumps the STA's messages. Its timer closes the dialog on this
	// same thread if the tab closes or the request expires.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil && err != syscall.Errno(1) {
		return "", err
	}
	defer windows.CoUninitialize()
	class := windows.GUID{Data1: 0xdc1c5a9c, Data2: 0xe88a, Data3: 0x4dde, Data4: [8]byte{0xa5, 0xa1, 0x60, 0xf8, 0x2a, 0x20, 0xae, 0xf7}}
	identifier := windows.GUID{Data1: 0xd57c7288, Data2: 0xd4ad, Data3: 0x4768, Data4: [8]byte{0xbe, 0x02, 0x9d, 0x96, 0x95, 0x32, 0xd9, 0x60}}
	var dialog *folderDialog
	result, _, _ := windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&class)), 0, 1, uintptr(unsafe.Pointer(&identifier)), uintptr(unsafe.Pointer(&dialog)))
	if err := folderResult("Create folder picker", result); err != nil {
		return "", err
	}
	defer syscall.SyscallN(dialog.methods.Release, uintptr(unsafe.Pointer(dialog)))
	// FOS_PICKFOLDERS | FOS_FORCEFILESYSTEM | FOS_PATHMUSTEXIST |
	// FOS_NOCHANGEDIR | FOS_DONTADDTORECENT.
	result, _, _ = syscall.SyscallN(dialog.methods.SetOptions, uintptr(unsafe.Pointer(dialog)), 0x20|0x40|0x800|0x8|0x02000000)
	if err := folderResult("Configure folder picker", result); err != nil {
		return "", err
	}
	title, _ := windows.UTF16PtrFromString("Choose projects folder")
	result, _, _ = syscall.SyscallN(dialog.methods.SetTitle, uintptr(unsafe.Pointer(dialog)), uintptr(unsafe.Pointer(title)))
	if err := folderResult("Name folder picker", result); err != nil {
		return "", err
	}
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return "", err
	}
	shellID := windows.GUID{Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee, Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}
	var initial *folderShellItem
	result, _, _ = windows.NewLazySystemDLL("shell32.dll").NewProc("SHCreateItemFromParsingName").Call(uintptr(unsafe.Pointer(path)), 0, uintptr(unsafe.Pointer(&shellID)), uintptr(unsafe.Pointer(&initial)))
	if err := folderResult("Read initial folder", result); err != nil {
		return "", err
	}
	defer syscall.SyscallN(initial.methods.Release, uintptr(unsafe.Pointer(initial)))
	result, _, _ = syscall.SyscallN(dialog.methods.SetFolder, uintptr(unsafe.Pointer(dialog)), uintptr(unsafe.Pointer(initial)))
	if err := folderResult("Set initial folder", result); err != nil {
		return "", err
	}
	owner := windows.GetForegroundWindow()
	ownerThread, _ := windows.GetWindowThreadProcessId(owner, nil)
	ownerDesktop, _, _ := folderUser32.NewProc("GetThreadDesktop").Call(uintptr(ownerThread))
	desktop, _, _ := folderUser32.NewProc("GetThreadDesktop").Call(uintptr(windows.GetCurrentThreadId()))
	if ownerDesktop != desktop {
		owner = 0
	}
	timer, _, timerErr := folderUser32.NewProc("SetTimer").Call(0, 0, 100, folderTimerCallback())
	if timer == 0 {
		return "", fmt.Errorf("watch folder picker: %w", timerErr)
	}
	folderTimersMu.Lock()
	folderTimers[timer] = &folderDialogWatch{ctx: ctx, owner: owner}
	folderTimersMu.Unlock()
	defer func() { folderTimersMu.Lock(); delete(folderTimers, timer); folderTimersMu.Unlock() }()
	defer folderUser32.NewProc("KillTimer").Call(0, timer)
	result, _, _ = syscall.SyscallN(dialog.methods.Show, uintptr(unsafe.Pointer(dialog)), uintptr(owner))
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if uint32(result) == FOLDER_DIALOG_CANCELLED {
		return "", nil
	}
	if err := folderResult("Show folder picker", result); err != nil {
		return "", err
	}
	var selected *folderShellItem
	result, _, _ = syscall.SyscallN(dialog.methods.GetResult, uintptr(unsafe.Pointer(dialog)), uintptr(unsafe.Pointer(&selected)))
	if err := folderResult("Read selected folder", result); err != nil {
		return "", err
	}
	defer syscall.SyscallN(selected.methods.Release, uintptr(unsafe.Pointer(selected)))
	var name *uint16
	result, _, _ = syscall.SyscallN(selected.methods.GetDisplayName, uintptr(unsafe.Pointer(selected)), 0x80058000, uintptr(unsafe.Pointer(&name)))
	if err := folderResult("Read selected path", result); err != nil {
		return "", err
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(name))
	return windows.UTF16PtrToString(name), nil
}
