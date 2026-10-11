package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/uninstall"
)

//go:embed uninstall.html
var uninstallPage string

// adminWait is what the window says while Windows asks for an administrator.
const adminWait = "Waiting for you to say yes to Windows' administrator prompt"

// runUninstall is the uninstall Windows' Installed apps opens: the plan the
// person reads first, then, once Uninstall is pressed, the same uninstall
// goblins uninstall runs, step by step, its check that nothing is left, and
// what is left if anything is, with how to finish. The window's own program,
// moved out of the home while it removes the home, and the browser profile it
// shows its page in go once it closes.
func runUninstall() int {
	machine, err := uninstall.ThisMachine()
	if err != nil {
		tell("Code Goblins could not be uninstalled, because what it holds could not be read: " + err.Error())
		return 1
	}
	profile, err := os.MkdirTemp("", "CodeGoblinsUninstall-")
	if err != nil {
		tell("Code Goblins could not be uninstalled, because Windows' temp folder could not be written: " + err.Error())
		return 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	plan, err := uninstall.NewPlan(ctx, machine, uninstall.Choices{})
	if err != nil {
		tell("Code Goblins could not be uninstalled, because what it holds could not be read: " + err.Error())
		return 1
	}

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
	var busy sync.Mutex
	var running sync.WaitGroup
	var asideMu sync.Mutex
	aside := ""
	kept := plan.Kept
	run := func(choices uninstall.Choices) {
		if !busy.TryLock() {
			return
		}
		running.Add(1)
		go func() {
			defer running.Done()
			defer busy.Unlock()
			current, err := uninstall.NewPlan(ctx, machine, choices)
			if err != nil {
				call("uninstallDone", "", []string{"The uninstall could not read what to remove: " + err.Error() + "."})
				return
			}
			call("uninstallStarted", current.Screen().Kept != "")
			outcome := current.Run(ctx, func(group uninstall.Group, _ []uninstall.Item, left []uninstall.Left, finished bool) {
				step := uninstall.StepOf(group)
				if !finished {
					doing := ""
					if group == uninstall.GroupDevDrive {
						doing = adminWait
					}
					call("uninstallStep", step, doing)
					return
				}
				for _, l := range left {
					call("uninstallFailed", step, l.Sentence())
				}
			})
			asideMu.Lock()
			aside = current.Aside()
			asideMu.Unlock()
			var left []string
			for _, l := range outcome.Left {
				left = append(left, l.Sentence())
			}
			if outcome.Stopped {
				left = append(left, "Nothing that holds your work was removed, since the work could not be kept.")
			}
			call("uninstallStep", uninstall.StepCheck, "")
			check := machine
			check.Aside = current.Aside()
			if again, err := uninstall.NewPlan(ctx, check, choices); err != nil {
				left = append(left, "The check that nothing is left could not run: "+err.Error()+".")
			} else {
				for _, item := range again.Remaining() {
					if !outcome.Named(item) {
						left = append(left, uninstall.Left{Item: item, Why: "it is still there"}.Sentence())
					}
				}
			}
			note := ""
			if kept = current.Screen().Kept; kept != "" {
				note = "Your work and fleet data are in " + kept + "."
			}
			if left == nil {
				left = []string{}
			}
			call("uninstallDone", note, left)
		}()
	}

	app = application.New(application.Options{
		Name:        "Code Goblins Uninstall",
		Description: "Uninstalls Code Goblins",
		Icon:        icon,
		// Its browser profile is a folder of its own, which goes once it
		// closes, never the window's own in the home.
		Windows:        application.WindowsOptions{WebviewUserDataPath: profile},
		SingleInstance: &application.SingleInstanceOptions{UniqueID: "dev.codegoblins.uninstall"},
		RawMessageHandler: func(_ application.Window, message string, _ *application.OriginInfo) {
			switch {
			case message == "uninstall:cancel" || message == "uninstall:close":
				app.Quit()
			case message == "uninstall:open-kept" && kept != "":
				if err := execx.Command("explorer.exe", kept).Start(); err != nil {
					log.Printf("open %s: %v", kept, err)
				}
			case strings.HasPrefix(message, "uninstall:run:"):
				work, tools, _ := strings.Cut(strings.TrimPrefix(message, "uninstall:run:"), ":")
				run(uninstall.Choices{RemoveWork: work == "on", RemoveTools: tools == "on"})
			}
		},
	})
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Uninstall Code Goblins",
		HTML:             strings.Replace(uninstallPage, `src="goblin.png"`, `src="data:image/png;base64,`+base64.StdEncoding.EncodeToString(icon)+`"`, 1),
		Width:            720,
		Height:           580,
		BackgroundColour: application.NewRGB(0x03, 0x05, 0x0a),
		Windows:          application.WindowsWindow{Theme: application.Dark},
	})
	// Wails holds a window's scripts until its own runtime says it is ready,
	// which this page, having none, never does; so the window says it for
	// the page once it has loaded, and shows the plan then.
	window.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) {
		window.HandleMessage("wails:runtime:ready")
		call("uninstallPlan", plan.Screen())
	})
	err = app.Run()
	cancel()
	running.Wait()
	if err != nil {
		log.Print(err)
	}
	asideMu.Lock()
	defer asideMu.Unlock()
	for _, folder := range []string{aside, profile} {
		if folder != "" {
			if err := uninstall.RemoveOnceEnded(folder); err != nil {
				log.Print(err)
			}
		}
	}
	return 0
}
