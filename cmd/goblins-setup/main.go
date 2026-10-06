// Command goblins-setup is CodeGoblinsSetup.exe, the one file a person
// downloads and opens to install Code Goblins with no terminal. It is only a
// window around the release's own install script, the one the one-line
// install runs: it says what the install will put where, runs the script out
// of sight once Install is pressed, shows its plain steps, says in one
// sentence why when it stops, and opens the app once it is done. So there is
// one install, whichever way it is started.
//
// Its manifest, in winres.json, says to run as the user who opened it, and to
// draw at each monitor's own scale. Without the first Windows takes a program
// whose name holds Setup for an installer and asks for an administrator, and
// the install, which is for one user, would go into the administrator's
// profile; without the second Windows draws the window at 100 percent and
// stretches it, blurred, on a display set larger.
package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

//go:embed page.html
var page string

//go:embed goblin.png
var goblin []byte

// shownEnd is how long the window says that Code Goblins is installed before
// it gives way to the app.
const shownEnd = 3 * time.Second

func main() {
	plan, err := findPlan()
	if err != nil {
		log.Fatal(err)
	}
	setup := Setup{
		Script: scriptURL(),
		Shell:  "powershell.exe",
		Log:    filepath.Join(os.TempDir(), "CodeGoblinsInstall.log"),
		Client: http.DefaultClient,
	}

	// Closing the window ends the install with it: app.Run returns, the
	// cancelled context ends the script and what it started, and the program
	// waits for that before it exits, so no install is left running unseen.
	ctx, cancel := context.WithCancel(context.Background())
	var running sync.WaitGroup
	var busy sync.Mutex
	var window *application.WebviewWindow
	var app *application.App
	call := func(function string, arguments ...any) {
		texts := make([]string, len(arguments))
		for i, argument := range arguments {
			text, _ := json.Marshal(argument)
			texts[i] = string(text)
		}
		window.ExecJS(function + "(" + strings.Join(texts, ",") + ")")
	}
	install := func() {
		if !busy.TryLock() {
			return
		}
		running.Add(1)
		call("setupStarted")
		go func() {
			defer running.Done()
			defer busy.Unlock()
			err := setup.Install(ctx, func(progress Progress) {
				switch {
				case progress.Step > 0:
					call("setupStep", progress.Step)
				case progress.Doing != "":
					call("setupDoing", progress.Doing)
				case progress.Note != "":
					call("setupNote", progress.Note)
				}
			})
			if err == nil {
				call("setupStep", 4)
				err = openApp(plan.Folder)
			}
			// A window that was closed has nothing left to tell.
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				call("setupFailed", err.Error(), setup.Details())
				return
			}
			call("setupDone")
			time.Sleep(shownEnd)
			app.Quit()
		}()
	}

	app = application.New(application.Options{
		Name:        "Code Goblins Setup",
		Description: "Installs Code Goblins",
		// The setup keeps no profile of its own once it has run: its WebView2
		// folder is in the temp folder, which Windows clears.
		Windows: application.WindowsOptions{WebviewUserDataPath: filepath.Join(os.TempDir(), "CodeGoblinsSetup")},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "dev.codegoblins.setup",
		},
		// The page's buttons post their names through the window's message
		// channel.
		RawMessageHandler: func(_ application.Window, message string, _ *application.OriginInfo) {
			switch message {
			case "setup:install":
				install()
			case "setup:cancel":
				app.Quit()
			case "setup:log":
				_ = execx.Command("notepad.exe", setup.Log).Start()
			}
		},
	})
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Code Goblins Setup",
		HTML:   strings.Replace(page, `src="goblin.png"`, `src="data:image/png;base64,`+base64.StdEncoding.EncodeToString(goblin)+`"`, 1),
		Width:  720,
		Height: 500,
		// The board's own dark ground, so the window never flashes white.
		BackgroundColour: application.NewRGB(0x03, 0x05, 0x0a),
		Windows:          application.WindowsWindow{Theme: application.Dark},
	})
	// Wails holds a window's scripts until its own runtime in the page says it
	// is ready, which this page, having none, never does; so the window says
	// it for the page once it has loaded, and shows the plan then.
	window.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) {
		window.HandleMessage("wails:runtime:ready")
		call("setupPlan", plan)
	})

	err = app.Run()
	cancel()
	running.Wait()
	if err != nil {
		log.Fatal(err)
	}
}
