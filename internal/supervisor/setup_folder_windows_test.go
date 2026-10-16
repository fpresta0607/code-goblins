package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestProjectsFolderPickerSelectsAndCancelsOnAHiddenDesktop(t *testing.T) {
	onAHiddenDesktop(t, func(t *testing.T) {
		for _, action := range []string{"select", "cancel", "disconnect"} {
			t.Run(action, func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "Selected projects 日本語")
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("CFO_HOME", t.TempDir())
				t.Setenv("CFO_STATE_OVERRIDE", t.TempDir())
				t.Setenv("CFO_PROJECTS_ROOT", t.TempDir())
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				type selection struct {
					path string
					err  error
				}
				result := make(chan selection, 1)
				go func() { path, err := pickProjectsFolder(ctx, root); result <- selection{path, err} }()
				user32 := windows.NewLazySystemDLL("user32.dll")
				desktop, _, _ := user32.NewProc("GetThreadDesktop").Call(uintptr(windows.GetCurrentThreadId()))
				var dialog uintptr
				callback := syscall.NewCallback(func(window, _ uintptr) uintptr {
					var pid uint32
					user32.NewProc("GetWindowThreadProcessId").Call(window, uintptr(unsafe.Pointer(&pid)))
					var title [128]uint16
					user32.NewProc("GetWindowTextW").Call(window, uintptr(unsafe.Pointer(&title[0])), 128)
					if pid == uint32(os.Getpid()) && windows.UTF16ToString(title[:]) == "Choose projects folder" && windows.IsWindowVisible(windows.HWND(window)) {
						dialog = window
					}
					return 1
				})
				for deadline := time.Now().Add(8 * time.Second); dialog == 0 && time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
					user32.NewProc("EnumDesktopWindows").Call(desktop, callback, 0)
				}
				if dialog == 0 {
					cancel()
					select {
					case got := <-result:
						t.Fatalf("no visible folder picker on the hidden desktop: %v", got.err)
					case <-time.After(5 * time.Second):
						t.Fatal("folder picker neither appeared nor stopped after cancellation")
					}
				}
				if action == "disconnect" {
					cancel()
				} else {
					button := uintptr(1)
					if action == "cancel" {
						button = 2
					}
					user32.NewProc("PostMessageW").Call(dialog, 0x111, button, 0)
				}
				select {
				case got := <-result:
					if action == "disconnect" {
						if !errors.Is(got.err, context.Canceled) {
							t.Fatalf("disconnect = %v", got.err)
						}
						return
					}
					if got.err != nil {
						t.Fatal(got.err)
					}
					if action == "select" && !strings.EqualFold(got.path, root) {
						t.Fatalf("selected %q, want %q", got.path, root)
					}
					if action == "cancel" && got.path != "" {
						t.Fatalf("cancel selected %q", got.path)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("folder picker did not close")
				}
			})
		}
	})
}
