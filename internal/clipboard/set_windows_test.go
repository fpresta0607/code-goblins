package clipboard

import (
	"bytes"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	createWindowStation     = user32.NewProc("CreateWindowStationW")
	getProcessWindowStation = user32.NewProc("GetProcessWindowStation")
	setProcessWindowStation = user32.NewProc("SetProcessWindowStation")
	closeWindowStation      = user32.NewProc("CloseWindowStation")
	getClipboardData        = user32.NewProc("GetClipboardData")
	clipboardSequence       = user32.NewProc("GetClipboardSequenceNumber")
	globalSize              = kernel32.NewProc("GlobalSize")
)

const stationAllAccess = 0x37f // WINSTA_ALL_ACCESS

// privateClipboard moves this test's process to a window station of its own,
// which holds a clipboard of its own, until the test ends: a run at a desk
// never touches the clipboard of the one sitting there. Each window station
// counts its own clipboard's changes, so a clipboard that counts differently
// from the one before the move is not the desk's. The station is this logon's
// own one for programs with no desk, which keeps what an earlier run put on
// its clipboard.
func privateClipboard(t *testing.T) {
	t.Helper()
	desk, _, _ := clipboardSequence.Call()
	original, _, err := getProcessWindowStation.Call()
	if original == 0 {
		t.Fatalf("read this process's window station: %v", err)
	}
	// A station with no name is this logon's own, which a user may make: a
	// named one needs rights a user lacks.
	station, _, err := createWindowStation.Call(0, 0, stationAllAccess, 0)
	if station == 0 {
		t.Fatalf("make a window station: %v", err)
	}
	if moved, _, err := setProcessWindowStation.Call(station); moved == 0 {
		closeWindowStation.Call(station)
		t.Fatalf("move to the window station: %v", err)
	}
	t.Cleanup(func() {
		if back, _, err := setProcessWindowStation.Call(original); back == 0 {
			t.Errorf("move back to the window station this process started in: %v", err)
		}
		closeWindowStation.Call(station)
	})
	if own, _, _ := clipboardSequence.Call(); own == desk {
		t.Fatalf("the clipboard after the move counts %d changes, as the desk's does, so it may be the desk's; nothing was put on it", own)
	}
}

// clipboardHolds reads what the clipboard holds in a format, as long as its
// memory is, which may run past what was put there.
func clipboardHolds(t *testing.T, format uintptr) []byte {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if opened, _, err := openClipboard.Call(0); opened == 0 {
		t.Fatalf("open the clipboard: %v", err)
	}
	defer closeClipboard.Call()
	memory, _, err := getClipboardData.Call(format)
	if memory == 0 {
		t.Fatalf("the clipboard holds nothing in format %d: %v", format, err)
	}
	size, _, _ := globalSize.Call(memory)
	address, _, err := globalLock.Call(memory)
	if address == 0 {
		t.Fatalf("read the clipboard's memory: %v", err)
	}
	defer globalUnlock.Call(memory)
	data := make([]byte, size)
	moveMemory.Call(uintptr(unsafe.Pointer(unsafe.SliceData(data))), address, size)
	return data
}

func TestSetImagePutsTheBitmapAndThePNGOnTheClipboard(t *testing.T) {
	// Arrange
	privateClipboard(t)
	picture, encoded := twoByTwo(t)
	name, err := windows.UTF16PtrFromString("PNG")
	if err != nil {
		t.Fatal(err)
	}
	formatPNG, _, _ := registerClipboardFormat.Call(uintptr(unsafe.Pointer(name)))

	// Act
	err = SetImage(encoded)

	// Assert
	if err != nil {
		t.Fatalf("SetImage = %v", err)
	}
	if held, want := clipboardHolds(t, formatBitmap), bitmap(picture); !bytes.HasPrefix(held, want) {
		t.Fatalf("the clipboard's bitmap = %v, want %v", held[:min(len(held), len(want))], want)
	}
	if held := clipboardHolds(t, formatPNG); !bytes.HasPrefix(held, encoded) {
		t.Fatal("the clipboard's PNG is not the picture put there")
	}
}
