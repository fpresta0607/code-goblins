package main

import (
	"log"
	"sync"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// noticeText is what the window says in place of a page that draws nothing
// while it loads the board again.
const noticeText = "The board stopped drawing. Loading it again."

// The notice's colours, as Windows takes them (0x00bbggrr): the board's own
// dark ground, the one the window shows before the board draws, and its muted
// text.
const (
	noticeGround = 0x18110b
	noticeInk    = 0xc2b3a3
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	gdi32            = windows.NewLazySystemDLL("gdi32.dll")
	registerClass    = user32.NewProc("RegisterClassExW")
	createWindow     = user32.NewProc("CreateWindowExW")
	defWindowProc    = user32.NewProc("DefWindowProcW")
	setWindowPos     = user32.NewProc("SetWindowPos")
	showWindow       = user32.NewProc("ShowWindow")
	getClientRect    = user32.NewProc("GetClientRect")
	invalidateRect   = user32.NewProc("InvalidateRect")
	beginPaint       = user32.NewProc("BeginPaint")
	endPaint         = user32.NewProc("EndPaint")
	fillRect         = user32.NewProc("FillRect")
	drawText         = user32.NewProc("DrawTextW")
	getDpiForWindow  = user32.NewProc("GetDpiForWindow")
	createSolidBrush = gdi32.NewProc("CreateSolidBrush")
	createFont       = gdi32.NewProc("CreateFontW")
	selectObject     = gdi32.NewProc("SelectObject")
	deleteObject     = gdi32.NewProc("DeleteObject")
	setTextColor     = gdi32.NewProc("SetTextColor")
	setBkMode        = gdi32.NewProc("SetBkMode")
)

type rect struct{ Left, Top, Right, Bottom int32 }

type paintStruct struct {
	HDC         uintptr
	Erase       int32
	Paint       rect
	Restore     int32
	IncUpdate   int32
	RGBReserved [32]byte
}

type windowClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

// noticeClass registers, once, the class the notice is made from, whose
// every paint is the ground and the one line, drawn by Windows' own GDI.
var noticeClass = sync.OnceValues(func() (*uint16, error) {
	name, err := windows.UTF16PtrFromString("CodeGoblinsNotice")
	if err != nil {
		return nil, err
	}
	text, err := windows.UTF16FromString(noticeText)
	if err != nil {
		return nil, err
	}
	face, err := windows.UTF16PtrFromString("Segoe UI")
	if err != nil {
		return nil, err
	}
	ground, _, _ := createSolidBrush.Call(noticeGround)
	paint := func(hwnd windows.HWND) {
		var ps paintStruct
		hdc, _, _ := beginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
		defer endPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
		var area rect
		getClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&area)))
		fillRect.Call(hdc, uintptr(unsafe.Pointer(&area)), ground)
		dpi, _, _ := getDpiForWindow.Call(uintptr(hwnd))
		if dpi == 0 {
			dpi = 96
		}
		// The board's body text, 16 px at 100 percent scale, in Windows' own
		// face, since the board's font is the page's.
		height := -int32(16 * dpi / 96)
		font, _, _ := createFont.Call(uintptr(height), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))
		if font != 0 {
			defer deleteObject.Call(font)
			previous, _, _ := selectObject.Call(hdc, font)
			defer selectObject.Call(hdc, previous)
		}
		setTextColor.Call(hdc, noticeInk)
		setBkMode.Call(hdc, 1)
		// DT_CENTER, DT_VCENTER and DT_SINGLELINE.
		drawText.Call(hdc, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)-1), uintptr(unsafe.Pointer(&area)), 0x1|0x4|0x20)
	}
	class := windowClass{
		WndProc: syscall.NewCallback(func(hwnd windows.HWND, message uint32, wParam, lParam uintptr) uintptr {
			switch message {
			case 0x000F: // WM_PAINT
				paint(hwnd)
				return 0
			case 0x0014: // WM_ERASEBKGND: the paint fills the whole notice.
				return 1
			}
			result, _, _ := defWindowProc.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
			return result
		}),
		ClassName: name,
	}
	class.Size = uint32(unsafe.Sizeof(class))
	if atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return nil, err
	}
	return name, nil
})

// Notice is the line the window shows in place of its page while it mends a
// page that draws nothing. Windows draws it, not WebView2, since the page it
// stands for may be one whose WebView2 cannot draw: a child window laid over
// the page, which it covers until the page draws again.
type Notice struct {
	hwnd windows.HWND
}

// Show shows the notice over window's page, or takes it away.
func (n *Notice) Show(window *application.WebviewWindow, on bool) {
	if !on && n.hwnd == 0 {
		return
	}
	parent := windows.HWND(window.NativeWindow())
	application.InvokeSync(func() {
		if !on {
			showWindow.Call(uintptr(n.hwnd), 0) // SW_HIDE
			return
		}
		if n.hwnd == 0 {
			class, err := noticeClass()
			if err != nil {
				log.Printf("show the window's notice: %v", err)
				return
			}
			// WS_CHILD and WS_CLIPSIBLINGS.
			hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(class)), 0, 0x40000000|0x04000000, 0, 0, 0, 0, uintptr(parent), 0, 0, 0)
			if hwnd == 0 {
				log.Printf("show the window's notice: %v", err)
				return
			}
			n.hwnd = windows.HWND(hwnd)
		}
		// Over the page, at the size of the window's inside, which the window
		// may have changed since the last look: SWP_SHOWWINDOW.
		var area rect
		getClientRect.Call(uintptr(parent), uintptr(unsafe.Pointer(&area)))
		setWindowPos.Call(uintptr(n.hwnd), 0, 0, 0, uintptr(area.Right), uintptr(area.Bottom), 0x0040)
		invalidateRect.Call(uintptr(n.hwnd), 0, 1)
	})
}
