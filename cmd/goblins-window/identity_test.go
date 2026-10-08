package main

import (
	"testing"
)

const (
	usersHome  = `C:\dev\code-goblins`
	usersLocal = `C:\Users\op\AppData\Local`
)

// The home the user's own environment names keeps the window's one instance
// and Windows' one notification registration for Code Goblins, so it opens
// and notifies as it always has, however its state folder is spelled.
func TestTheUsersOwnHomesWindowKeepsTheOneInstanceAndItsNotifications(t *testing.T) {
	for name, test := range map[string]struct {
		userEnv  []string
		stateDir string
	}{
		"named by CFO_HOME":                     {[]string{"CFO_HOME=" + usersHome, "LOCALAPPDATA=" + usersLocal}, usersHome + `\state`},
		"named by CFO_HOME, spelled in caps":    {[]string{"cfo_home=" + usersHome}, `C:\DEV\Code-Goblins\state\`},
		"the per-user home":                     {[]string{"LOCALAPPDATA=" + usersLocal}, usersLocal + `\CodeGoblins\state`},
		"its state named by CFO_STATE_OVERRIDE": {[]string{"CFO_HOME=" + usersHome, `CFO_STATE_OVERRIDE=D:\fleet-state`}, `D:\fleet-state`},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			window := windowIdentity(test.stateDir, "", test.userEnv)

			// Assert
			if window.instance != "dev.codegoblins.window" || !window.notifies {
				t.Errorf("identity = %+v, want the one instance, raising notifications", window)
			}
		})
	}
}

// A second home's window, such as a scratch home's or a second install's, is
// an instance of its own, so opening it brings its own board to the front and
// never the user's own home's window, and it raises no Windows notification,
// whose one registration would hand it the clicks on the user's own home's.
// Both homes' windows shared one instance id, so with the first home's window
// open, the second home's focused it instead of opening its own board.
func TestASecondHomesWindowIsAnInstanceOfItsOwn(t *testing.T) {
	// Arrange
	userEnv := []string{"CFO_HOME=" + usersHome, "LOCALAPPDATA=" + usersLocal}
	scratch := `C:\Users\op\AppData\Local\Temp\proof\home\state`

	// Act
	users := windowIdentity(usersHome+`\state`, "", userEnv)
	second := windowIdentity(scratch, "", userEnv)
	again := windowIdentity(`c:\users\OP\AppData\Local\Temp\proof\home\state\`, "", userEnv)
	third := windowIdentity(`C:\Users\op\AppData\Local\Temp\proof-2\home\state`, "", userEnv)

	// Assert
	if second.instance == users.instance || third.instance == users.instance || second.instance == third.instance {
		t.Errorf("instances = %q, %q and %q, want one for each home", users.instance, second.instance, third.instance)
	}
	if again.instance != second.instance {
		t.Errorf("one home's state spelled two ways has instances %q and %q, want one", second.instance, again.instance)
	}
	if second.notifies || third.notifies {
		t.Error("a second home's window raises Windows notifications, taking the user's own home's registration")
	}
}

// A window on a profile of its own, as the window's tests start, is an
// instance of its own beside the user's window and raises no notification.
func TestAWindowOnAProfileOfItsOwnIsAnInstanceOfItsOwn(t *testing.T) {
	userEnv := []string{"CFO_HOME=" + usersHome}

	window := windowIdentity(usersHome+`\state`, `C:\tmp\profile`, userEnv)

	if window.instance == "dev.codegoblins.window" || window.notifies {
		t.Errorf("identity = %+v, want an instance of its own, raising no notification", window)
	}
}
