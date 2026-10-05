// Command goblins-setup is CodeGoblinsSetup.exe, the one file a person
// downloads and opens to install Code Goblins with no terminal. It is only a
// window around the release's own install script, the one the one-line
// install runs: it downloads that script, runs it out of sight, shows each
// line it prints, says plainly why when it stops, and opens the app once it
// is done. So there is one install, whichever way it is started.
//
// Its manifest, in winres.json, says to run as the user who opened it.
// Without that Windows takes a program whose name holds Setup for an
// installer and asks for an administrator, and the install, which is for one
// user, would go into the administrator's profile.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed page.html
var page string

// shownEnd is how long the window says that Code Goblins is installed before
// it gives way to the app.
const shownEnd = 2 * time.Second

func main() {
	// The home the install script sets up, where it puts the app.
	home := filepath.Join(os.Getenv("LOCALAPPDATA"), "CodeGoblins")
	setup := Setup{
		Script: scriptURL(),
		Shell:  "powershell.exe",
		Log:    filepath.Join(os.TempDir(), "CodeGoblinsSetup.log"),
		Client: http.DefaultClient,
	}

	app := application.New(application.Options{
		Name:        "Code Goblins Setup",
		Description: "Installs Code Goblins",
		// The setup keeps no profile of its own once it has run: its WebView2
		// folder is in the temp folder, which Windows clears.
		Windows: application.WindowsOptions{WebviewUserDataPath: filepath.Join(os.TempDir(), "CodeGoblinsSetup")},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "dev.codegoblins.setup",
		},
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Code Goblins Setup",
		HTML:   page,
		Width:  760,
		Height: 520,
		// The board's own dark ground, so the window never flashes white.
		BackgroundColour: application.NewRGB(0x0b, 0x11, 0x18),
		Windows:          application.WindowsWindow{Theme: application.Dark},
	})
	call := func(function string, arguments ...string) {
		script := function + "("
		for i, argument := range arguments {
			text, _ := json.Marshal(argument)
			if i > 0 {
				script += ","
			}
			script += string(text)
		}
		window.ExecJS(script + ")")
	}

	// Closing the window ends the install with it: app.Run returns, the
	// cancelled context ends the script and what it started, and the program
	// waits for that before it exits, so no install is left running unseen.
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	ended := make(chan struct{})
	// Wails holds a window's scripts until its own runtime in the page says it
	// is ready, which this page, having none, never does; so the window says
	// it for the page once it has loaded, and the install starts then.
	window.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) {
		window.HandleMessage("wails:runtime:ready")
		once.Do(func() {
			go func() {
				err := setup.Install(ctx, func(line string) { call("setupLine", line) })
				if err == nil {
					err = openApp(home)
				}
				close(ended)
				// A window that was closed has nothing left to tell.
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					call("setupFailed", err.Error(), setup.Log)
					return
				}
				call("setupDone", "Opening Code Goblins ...")
				time.Sleep(shownEnd)
				app.Quit()
			}()
		})
	})

	err := app.Run()
	cancel()
	// An install that never began has nothing to wait for.
	once.Do(func() { close(ended) })
	<-ended
	if err != nil {
		log.Fatal(err)
	}
}
