package clipboard

import (
	"errors"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	openClipboard           = user32.NewProc("OpenClipboard")
	closeClipboard          = user32.NewProc("CloseClipboard")
	emptyClipboard          = user32.NewProc("EmptyClipboard")
	setClipboardData        = user32.NewProc("SetClipboardData")
	registerClipboardFormat = user32.NewProc("RegisterClipboardFormatW")
	globalAlloc             = kernel32.NewProc("GlobalAlloc")
	globalLock              = kernel32.NewProc("GlobalLock")
	globalUnlock            = kernel32.NewProc("GlobalUnlock")
	globalFree              = kernel32.NewProc("GlobalFree")
	moveMemory              = kernel32.NewProc("RtlMoveMemory")
)

const (
	formatBitmap = 8 // CF_DIB
	moveable     = 0x0002
	// Another program may hold the clipboard for a moment, as one reading
	// it does; opening is tried this often, this far apart.
	openTries = 10
	openPause = 20 * time.Millisecond
)

// set puts a bitmap and its PNG on the clipboard. Opening, filling and
// closing the clipboard happen on one thread, as Windows requires.
func set(bitmap, picture []byte) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	isOpen := false
	for try := 0; try < openTries && !isOpen; try++ {
		if try > 0 {
			time.Sleep(openPause)
		}
		opened, _, _ := openClipboard.Call(0)
		isOpen = opened != 0
	}
	if !isOpen {
		return errors.New("another program holds the clipboard")
	}
	defer closeClipboard.Call()
	if emptied, _, err := emptyClipboard.Call(); emptied == 0 {
		return fmt.Errorf("empty the clipboard: %w", err)
	}
	name, err := windows.UTF16PtrFromString("PNG")
	if err != nil {
		return err
	}
	formatPNG, _, err := registerClipboardFormat.Call(uintptr(unsafe.Pointer(name)))
	if formatPNG == 0 {
		return fmt.Errorf("name the clipboard's PNG format: %w", err)
	}
	if err := put(formatBitmap, bitmap); err != nil {
		return err
	}
	return put(formatPNG, picture)
}

// put hands the open clipboard a copy of data in a format. The clipboard
// owns the copy once it takes it.
func put(format uintptr, data []byte) error {
	memory, _, err := globalAlloc.Call(moveable, uintptr(len(data)))
	if memory == 0 {
		return fmt.Errorf("make room for the picture: %w", err)
	}
	address, _, err := globalLock.Call(memory)
	if address == 0 {
		globalFree.Call(memory)
		return fmt.Errorf("make room for the picture: %w", err)
	}
	moveMemory.Call(address, uintptr(unsafe.Pointer(unsafe.SliceData(data))), uintptr(len(data)))
	globalUnlock.Call(memory)
	if taken, _, err := setClipboardData.Call(format, memory); taken == 0 {
		globalFree.Call(memory)
		return fmt.Errorf("put the picture on the clipboard: %w", err)
	}
	return nil
}
