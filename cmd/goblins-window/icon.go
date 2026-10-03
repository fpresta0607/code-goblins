package main

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
	"golang.org/x/sys/windows/registry"
)

// notifyKey is where Windows keeps, for the user, the name and the picture
// of the program that raises Code Goblins' notifications; tests point it at a
// key of their own.
var notifyKey = `Software\Classes\AppUserModelId\Code Goblins`

// pictureName is the file that holds the picture of the window's
// notifications, beside the program.
const pictureName = "goblins-window.png"

// keepPicture puts picture beside program and names that file to Windows as
// the picture of the program's notifications, and returns the file's path.
// The path comes back whenever the file is there, also with the error of a
// registration that could not be made, so a notification still carries it.
// Each start does it, so a picture that went missing and a registration that
// names another place are put right. Left to the notifications' library the
// picture lives in the temp folder and is written only with a notification:
// the registration names a file that is not there until the first one, and
// that goes whenever the folder is cleared.
func keepPicture(program string, picture []byte) (string, error) {
	path := filepath.Join(filepath.Dir(program), pictureName)
	if held, err := fsx.ReadFile(path); err != nil || !bytes.Equal(held, picture) {
		if err := os.WriteFile(path, picture, 0o644); err != nil {
			return "", err
		}
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, notifyKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return path, err
	}
	defer key.Close()
	if named, _, err := key.GetStringValue("IconUri"); err == nil && named == path {
		return path, nil
	}
	return path, key.SetStringValue("IconUri", path)
}

// pictured is the picture at path as a notification carries it, so each one
// shows it whatever Windows holds of the program's own picture. With no path
// a notification carries none.
func pictured(path string) []notifications.NotificationAttachment {
	if path == "" {
		return nil
	}
	address := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}
	return []notifications.NotificationAttachment{{Path: address.String(), Type: "appLogoOverride"}}
}
