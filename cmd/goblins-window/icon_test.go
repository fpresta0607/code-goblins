package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// ownNotifyKey points the window's registration for notifications at a key
// of this test's own, never the user's.
func ownNotifyKey(t *testing.T) {
	t.Helper()
	var name [8]byte
	if _, err := rand.Read(name[:]); err != nil {
		t.Fatal(err)
	}
	notifyKey = `Software\CodeGoblinsTest\` + hex.EncodeToString(name[:])
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, notifyKey)
		// Removed only once no other run's key is under it.
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\CodeGoblinsTest`)
		notifyKey = `Software\Classes\AppUserModelId\Code Goblins`
	})
}

// The picture of the window's notifications lives beside the program and
// Windows is told of it there, so it is there after a start and after the
// temp folder is cleared. A start puts right what it finds: no picture, a
// registration that names the temp folder as the one before this did, or the
// picture of another build.
func TestAStartKeepsTheNotificationsPictureBesideTheProgram(t *testing.T) {
	picture := []byte("the goblin")
	for name, arrange := range map[string]func(t *testing.T, beside, temp string){
		"a first start": func(*testing.T, string, string) {},
		"a registration that names the temp folder": func(t *testing.T, _, temp string) {
			register(t, filepath.Join(temp, "Code Goblins{675d0139-5ced-40b2-8c52-7ff0dca6fc9d}.png"))
		},
		"a picture that went missing": func(t *testing.T, beside, _ string) { register(t, beside) },
		"the picture of another build": func(t *testing.T, beside, _ string) {
			register(t, beside)
			if err := os.WriteFile(beside, []byte("another goblin"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			ownNotifyKey(t)
			folder, temp := t.TempDir(), t.TempDir()
			t.Setenv("TEMP", temp)
			t.Setenv("TMP", temp)
			beside := filepath.Join(folder, "goblins-window.png")
			arrange(t, beside, temp)

			// Act
			path, err := keepPicture(filepath.Join(folder, "goblins-window.exe"), picture)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(temp); err != nil {
				t.Fatal(err)
			}

			// Assert
			registered := registeredPicture(t)
			if path != beside || registered != beside {
				t.Errorf("keepPicture = %s and Windows is told %s; want both %s", path, registered, beside)
			}
			if held, err := os.ReadFile(registered); err != nil || !bytes.Equal(held, picture) {
				t.Errorf("the registered picture holds %q (%v) once the temp folder is cleared, want the program's picture", held, err)
			}
		})
	}
}

// A registration that cannot be made does not take the picture from a
// notification: the picture is beside the program and its path comes back
// with the error. Windows makes no key whose name is as long as the one
// here.
func TestAPictureThatCannotBeRegisteredIsStillKept(t *testing.T) {
	// Arrange
	picture := []byte("the goblin")
	notifyKey = `Software\CodeGoblinsTest\` + strings.Repeat("k", 300)
	t.Cleanup(func() {
		// Removed only once no other run's key is under it.
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\CodeGoblinsTest`)
		notifyKey = `Software\Classes\AppUserModelId\Code Goblins`
	})
	folder := t.TempDir()
	beside := filepath.Join(folder, "goblins-window.png")

	// Act
	path, err := keepPicture(filepath.Join(folder, "goblins-window.exe"), picture)

	// Assert
	if err == nil || path != beside {
		t.Errorf("keepPicture = %q, %v; want %s and the error of the registration", path, err, beside)
	}
	if held, err := os.ReadFile(beside); err != nil || !bytes.Equal(held, picture) {
		t.Errorf("the picture beside the program holds %q (%v), want the program's picture", held, err)
	}
}

// A notification carries the picture as a file address Windows reads, with
// what an address cannot hold as it is, such as a space, escaped; with no
// picture it carries none.
func TestANotificationCarriesThePictureAsAFileAddress(t *testing.T) {
	carried, none := pictured(`C:\Users\some one\AppData\Local\CodeGoblinsWindow\goblins-window.png`), pictured("")

	if len(carried) != 1 || carried[0].Path != "file:///C:/Users/some%20one/AppData/Local/CodeGoblinsWindow/goblins-window.png" || carried[0].Type != "appLogoOverride" {
		t.Errorf("pictured = %+v, want one appLogoOverride picture at the file's address", carried)
	}
	if len(none) != 0 {
		t.Errorf("pictured with no path = %+v, want none", none)
	}
}

// register names path to Windows as the notifications' picture, as an
// earlier start did.
func register(t *testing.T, path string) {
	t.Helper()
	key, _, err := registry.CreateKey(registry.CURRENT_USER, notifyKey, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	if err := key.SetStringValue("IconUri", path); err != nil {
		t.Fatal(err)
	}
}

// registeredPicture is the picture Windows is told of.
func registeredPicture(t *testing.T) string {
	t.Helper()
	key, err := registry.OpenKey(registry.CURRENT_USER, notifyKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	path, _, err := key.GetStringValue("IconUri")
	if err != nil {
		t.Fatal(err)
	}
	return path
}
