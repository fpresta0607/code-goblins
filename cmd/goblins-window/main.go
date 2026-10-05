// Command goblins-window is the Code Goblins desktop window: a Wails window
// around the board cfo serve already serves, with a tray icon, notifications,
// one instance and start at login. It holds no engine logic, so closing it
// leaves the supervisor, the CFO and every goblin running. Started with the
// board's address and the fleet's state folder it is the window; started with
// neither, as the Start menu and Start at login start it, it opens the app by
// running the goblins beside it, which starts it again on the board.
package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed goblin.png
var icon []byte

// startGrace is how long after its start the window leaves its page alone.
const startGrace = 20 * time.Second

func main() {
	board := flag.String("board", "", "the board's URL")
	stateDir := flag.String("state", "", "the fleet's state folder, where the supervisor records its board")
	background := flag.Bool("background", false, "start in the tray without showing the window")
	profile := flag.String("profile", "", "the WebView2 profile folder, in place of the user's own; a window on a profile of its own is an instance of its own and raises no Windows notification")
	browserArgs := flag.String("webview-args", "", "more arguments for the WebView2 browser, separated by spaces, as the window's tests need for its DevTools port")
	// Windows starts the program with -Embedding when a notification is
	// clicked while no window runs. It is then started alone, and opens the
	// app as it does from the Start menu.
	flag.Bool("Embedding", false, "given by Windows when a click on a notification starts the program")
	flag.Parse()
	self, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	if *board == "" && *stateDir == "" {
		if message := launch(self, *background); message != "" {
			tell(message)
			os.Exit(1)
		}
		return
	}
	if *board == "" || *stateDir == "" {
		fmt.Fprintln(os.Stderr, "goblins-window: --board and --state go together; with neither, the window runs the goblins beside it")
		os.Exit(2)
	}
	launcher := os.Getenv(launcherVariable)
	login := loginCommand(launcher, self, *board, *stateDir)
	// The window's tests run on a profile of their own, beside the user's
	// window and never as its second instance.
	instance := "dev.codegoblins.window"
	var windows application.WindowsOptions
	if *profile != "" {
		sum := sha256.Sum256([]byte(*profile))
		instance += "." + hex.EncodeToString(sum[:8])
		windows.WebviewUserDataPath = *profile
	}
	windows.AdditionalBrowserArgs = strings.Fields(*browserArgs)

	var window *application.WebviewWindow
	show := func() {
		if window != nil {
			window.Show()
			window.Restore()
			window.Focus()
		}
	}
	// A window on a profile of its own, as the window's tests start, claims
	// nothing from its board and raises no Windows notification: registering
	// for them names this program to Windows as the one a click on the user's
	// own window's notifications starts, and a claim it raises nothing for
	// takes the alert from a page on that board.
	var notifier *Notifier
	var services []application.Service
	if *profile == "" {
		toasts := notifications.New()
		services = append(services, application.NewService(toasts))
		notifier = &Notifier{
			Away:   func() bool { return away(window) },
			Client: &http.Client{Timeout: claimWait},
			// The library refuses a notification whose picture is not there,
			// so each one puts a missing picture back and goes without one
			// when it cannot be kept.
			Send: func(note Note) error {
				picture, err := keepPicture(self, icon)
				if err != nil {
					log.Printf("keep the notifications' picture: %v", err)
				}
				return toasts.SendNotification(notifications.NotificationOptions{ID: note.ID, Title: note.Title, Body: note.Body, Attachments: pictured(picture)})
			},
			Wait: time.Sleep,
		}
		// Clicking a notification brings the window to the front, and the
		// board's page, where it was handed the alert, opens what it was about.
		toasts.OnNotificationResponse(func(result notifications.NotificationResult) {
			if result.Error != nil {
				log.Printf("a clicked notification could not be read: %v", result.Error)
				return
			}
			show()
			window.ExecJS(noteClicked(result.Response.ID))
		})
	}
	page := &Page{}
	app := application.New(application.Options{
		Name:        "Code Goblins",
		Description: "The Code Goblins board",
		Icon:        icon,
		Services:    services,
		Windows:     windows,
		// What the board's page asks of the window: that it is there, each
		// link it would open in a new tab, and each notification it raises.
		RawMessageHandler: func(_ application.Window, message string, sender *application.OriginInfo) {
			if message == shownMessage && fromBoard(sender.Origin) {
				page.Shown()
			} else if address, ok := linkToOpen(message, sender.Origin); ok {
				if err := openInBrowser(address); err != nil {
					log.Printf("open %s in the browser: %v", address, err)
				}
			} else if note, ok := noteToRaise(message, sender.Origin); ok && notifier != nil {
				notifier.FromPage(note)
			}
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: instance,
			// A second goblins brings this window to the front.
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { show() },
		},
	})
	// A second start ends in application.New. Only the window that runs keeps
	// the picture: another copy of the program, started beside it, must not
	// name its own folder to Windows as where the picture is.
	if notifier != nil {
		if _, err := keepPicture(self, icon); err != nil {
			log.Printf("keep the notifications' picture: %v", err)
		}
	}
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Code Goblins",
		URL:    *board,
		Width:  1600,
		Height: 1000,
		Hidden: *background,
		// The board's own dark ground, so the window never flashes white
		// before the board draws, under a dark title bar.
		BackgroundColour: application.NewRGB(0x0b, 0x11, 0x18),
		Windows:          application.WindowsWindow{Theme: application.Dark},
	})
	// Closing hides the window to the tray; Quit in the tray ends it.
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		window.Hide()
		e.Cancel()
	})
	// Wails holds a window's scripts until its own runtime in the page says it
	// is ready, which the board's page, not being a Wails page, never does;
	// so the window says it for the page once each load completes.
	window.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) {
		window.HandleMessage("wails:runtime:ready")
		window.ExecJS(linksScript + notesScript + speechScript)
	})

	menu := app.NewMenu()
	menu.Add("Open the board").OnClick(func(*application.Context) { show() })
	if adopted, err := adoptEarlierLogin(launcher, self, login); err != nil {
		log.Printf("start at login: %v", err)
	} else if adopted {
		log.Printf("start at login now runs %s", login)
	}
	atLogin := menu.AddCheckbox("Start at login", StartsAtLogin(login))
	atLogin.OnClick(func(ctx *application.Context) {
		if err := SetStartAtLogin(login, atLogin.Checked()); err != nil {
			atLogin.SetChecked(!atLogin.Checked())
			log.Printf("start at login: %v", err)
		}
	})
	menu.AddSeparator()
	menu.Add("Quit the window").OnClick(func(*application.Context) { app.Quit() })
	tray := app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip("Code Goblins")
	tray.SetMenu(menu)
	tray.OnClick(show)

	follow := &Follower{StateDir: *stateDir, Current: *board}
	watcher := &Watcher{}
	started := time.Now()
	go func() {
		for range time.Tick(3 * time.Second) {
			// Wails loads the window's first page itself, and until its
			// WebView2 exists there is nothing to load a page into.
			if time.Since(started) < startGrace {
				continue
			}
			url, moved := follow.Next()
			fresh, answers := watcher.New(url)
			if moved {
				window.SetURL(url)
			} else {
				switch page.Look(answers) {
				case Ask:
					window.ExecJS(shownScript)
				case Load:
					window.SetURL(url)
				}
			}
			// What newly waits on the Overlord is told from here while he
			// cannot see the board: its page, which Windows runs at the lowest
			// priority while it is hidden, tells it late.
			if notifier != nil {
				go notifier.FromBoard(url, watcher.Instance, fresh)
			}
		}
	}()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
