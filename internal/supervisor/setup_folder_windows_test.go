package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/awake"
)

func waitForFolderPickerReady(ctx context.Context, dialog uintptr) error {
	user32 := windows.NewLazySystemDLL("user32.dll")
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("folder picker OK button did not become ready: %w", err)
		}
		button, _, _ := user32.NewProc("GetDlgItem").Call(dialog, 1)
		isEnabled, _, _ := user32.NewProc("IsWindowEnabled").Call(button)
		if button != 0 && isEnabled != 0 && windows.IsWindowVisible(windows.HWND(button)) {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func TestProjectsFolderPickerSelectsAndCancelsOnAHiddenDesktop(t *testing.T) {
	onAHiddenDesktop(t, func(t *testing.T) {
		for _, action := range []string{"select", "select_after_ready", "never_ready", "cancel", "disconnect"} {
			t.Run(action, func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "Selected projects 日本語")
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("CFO_HOME", t.TempDir())
				t.Setenv("CFO_STATE_OVERRIDE", t.TempDir())
				t.Setenv("CFO_PROJECTS_ROOT", t.TempDir())
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				type selection struct {
					path string
					err  error
				}
				result := make(chan selection, 1)
				stopped := make(chan struct{})
				go func() {
					defer close(stopped)
					path, err := pickProjectsFolder(ctx, root)
					result <- selection{path, err}
				}()
				defer func() {
					cancel()
					cleanup, stopCleanup := awake.WithTimeout(context.Background(), 5*time.Second)
					defer stopCleanup()
					select {
					case <-stopped:
					case <-cleanup.Done():
						t.Error("folder picker did not stop after cancellation")
					}
				}()
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
				for deadline := time.Now().Add(time.Minute); dialog == 0 && time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
					user32.NewProc("EnumDesktopWindows").Call(desktop, callback, 0)
				}
				if dialog == 0 {
					t.Fatal("no visible folder picker on the hidden desktop")
				}
				shouldSelect := action == "select" || action == "select_after_ready"
				if shouldSelect || action == "never_ready" {
					readyContext, stopWaiting := awake.WithTimeout(ctx, 10*time.Second)
					defer stopWaiting()
					if err := waitForFolderPickerReady(readyContext, dialog); err != nil {
						t.Fatal(err)
					}
					if action == "select_after_ready" || action == "never_ready" {
						button, _, _ := user32.NewProc("GetDlgItem").Call(dialog, 1)
						if button == 0 {
							t.Fatal("ready dialog has no OK button")
						}
						user32.NewProc("EnableWindow").Call(button, 0)
						if !windows.IsWindowVisible(windows.HWND(dialog)) {
							t.Fatal("disabled OK button hid the dialog")
						}
						if action == "never_ready" {
							// stall-accepted: the disabled control is meant to outlast this deadline.
							deadline, stop := context.WithTimeout(ctx, 250*time.Millisecond)
							defer stop()
							if err := waitForFolderPickerReady(deadline, dialog); !errors.Is(err, context.DeadlineExceeded) {
								t.Fatalf("never-ready picker = %v, want deadline exceeded", err)
							}
							return
						}
						ready := make(chan error, 1)
						go func() { ready <- waitForFolderPickerReady(readyContext, dialog) }()
						select {
						case err := <-ready:
							t.Fatalf("visible picker with disabled OK was treated as ready: %v", err)
						case <-time.After(100 * time.Millisecond): // stall-accepted: nothing should report ready while OK is disabled; a stall only lengthens this observation.
						}
						user32.NewProc("EnableWindow").Call(button, 1)
						if err := <-ready; err != nil {
							t.Fatal(err)
						}
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
				completion, stopCompletion := awake.WithTimeout(context.Background(), 10*time.Second)
				defer stopCompletion()
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
					if shouldSelect {
						wanted, err := os.Stat(root)
						if err != nil {
							t.Fatal(err)
						}
						selected, err := os.Stat(got.path)
						if err != nil {
							t.Fatal(err)
						}
						if !os.SameFile(wanted, selected) {
							t.Fatalf("selected %q, want %q", got.path, root)
						}
					}
					if action == "cancel" && got.path != "" {
						t.Fatalf("cancel selected %q", got.path)
					}
				case <-completion.Done():
					t.Fatal("folder picker did not close after one command")
				}
			})
		}
	})
}
