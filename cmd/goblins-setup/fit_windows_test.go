package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// Every screen of the setup's page fits the window at 100, 125, 150 and 200
// percent display scaling: nothing needs scrolling or runs past a side, every
// button is inside the window, and a row the page hides shows nothing. The
// page is drawn by a headless Google Chrome, whose engine, Chromium, the
// window's WebView2 is, at the client area and scale Windows gives the
// setup's window at each DPI. Until this test the first page ran past the
// window's bottom, Install and Cancel with it, and the Dev Drive row showed
// its label when hidden.
func TestEveryScreenFitsTheWindowAtEachScale(t *testing.T) {
	// Arrange
	page := openSetupPage(t)
	fresh := Plan{Folder: `C:\Users\Alexandra\AppData\Local\CodeGoblins`, StartAtLogin: true, DevDriveOffer: true, DevDriveExplain: devdrive.Explain}
	noDevDrive := fresh
	noDevDrive.DevDriveOffer = false
	kept := fresh
	kept.Folder, kept.Update, kept.Note = `D:\Projects\code-goblins`, true, keptNote
	// The notes saying why there is no Dev Drive are the setup's own: on
	// Windows 10, and where Defender could not be read, which carries the
	// whole of PowerShell's error record.
	windows10 := devdrive.Machine{Build: 19045, Revision: 6456, Defender: true, SystemDrive: "C:", SystemFree: 374 << 30}
	defenderUnread := devdrive.Machine{Build: 26200, Revision: 9457, SystemDrive: "C:", SystemFree: 374 << 30,
		DefenderUnread: "Get-MpComputerStatus: Get-MpComputerStatus : A general error occurred that is not covered by a more specific error code.\r\n" +
			"At line:1 char:2\r\n" +
			"+ (Get-MpComputerStatus).RealTimeProtectionEnabled\r\n" +
			"+  ~~~~~~~~~~~~~~~~~~~~\r\n" +
			"    + CategoryInfo          : NotSpecified: (MSFT_MpComputerStatus:ROOT\\Microsoft\\...pComputerStatus) [Get-MpComputerStatus], CimException\r\n" +
			"    + FullyQualifiedErrorId : HRESULT 0x800106ba,Get-MpComputerStatus"}
	onWindows10, withDefenderUnread := fresh, fresh
	onWindows10.DevDriveOffer, onWindows10.DevDriveNote = devDriveOffer(windows10, home.DevDriveConfig{}, fresh.Folder)
	withDefenderUnread.DevDriveOffer, withDefenderUnread.DevDriveNote = devDriveOffer(defenderUnread, home.DevDriveConfig{}, fresh.Folder)
	log := strings.Repeat("install  chrome-devtools-axi  npm.cmd install -g chrome-devtools-axi@0.1.39 --no-audit --no-fund\n", 40)
	screens := map[string][]string{
		"the plan with the Dev Drive offer":           {pageCall("setupPlan", fresh)},
		"the plan with no Dev Drive row":              {pageCall("setupPlan", noDevDrive)},
		"the update of a home kept where it is":       {pageCall("setupPlan", kept)},
		"the plan saying Windows 10 has no Dev Drive": {pageCall("setupPlan", onWindows10)},
		"the plan saying Defender could not be read":  {pageCall("setupPlan", withDefenderUnread)},
		"the install under way":                       {pageCall("setupPlan", fresh), pageCall("setupStarted"), pageCall("setupRelease", "v0.12.345"), pageCall("setupStep", 3), pageCall("setupDoing", "Installing the GitHub CLI")},
		"the update of a kept home under way":         {pageCall("setupPlan", kept), pageCall("setupStarted"), pageCall("setupRelease", "v0.12.345"), pageCall("setupStep", 3), pageCall("setupDoing", "Installing the GitHub CLI")},
		"the update of a kept home done":              {pageCall("setupPlan", kept), pageCall("setupStarted"), pageCall("setupRelease", "v0.12.345"), pageCall("setupDone")},
		"a failed install with its details shown": {pageCall("setupPlan", fresh), pageCall("setupStarted"), pageCall("setupStep", 3),
			pageCall("setupFailed", "Code Goblins could not be set up: the home's state folder could not be written; free some disk space, then try again.", log),
			`document.getElementById("show-details").click()`},
		"the install done with a note": {pageCall("setupPlan", fresh), pageCall("setupStarted"), pageCall("setupRelease", "v0.12.345"),
			pageCall("setupNote", "These could not be installed, so only what needs them is left out: codex, pi, chrome-devtools-axi skill. The log says why."), pageCall("setupDone")},
	}

	for _, dpi := range []uint32{96, 120, 144, 192} {
		width, height := clientSize(t, dpi)
		for name, script := range screens {
			t.Run(fmt.Sprintf("%s at %d percent", name, dpi*100/96), func(t *testing.T) {
				// Act
				problems := page.show(t, width, height, float64(dpi)/96, strings.Join(script, ";"))

				// Assert
				for _, problem := range problems {
					t.Errorf("in a %dx%d window: %s", width, height, problem)
				}
			})
		}
	}
}

// clientSize is the client area, in device-independent pixels rounded down,
// that Windows gives the setup's window at dpi: the window is windowWidth by
// windowHeight at every scale, and its frame is the overlapped window's that
// Wails makes.
func clientSize(t *testing.T, dpi uint32) (int, int) {
	t.Helper()
	var frame windows.Rect
	const overlappedWindow, controlParentAppWindow = 0x00CF0000, 0x00050000
	ok, _, err := windows.NewLazySystemDLL("user32.dll").NewProc("AdjustWindowRectExForDpi").Call(uintptr(unsafe.Pointer(&frame)), overlappedWindow, 0, controlParentAppWindow, uintptr(dpi))
	if ok == 0 {
		t.Fatalf("AdjustWindowRectExForDpi at %d DPI: %v", dpi, err)
	}
	scale := float64(dpi) / 96
	width := math.Round(windowWidth*scale) - float64(frame.Right-frame.Left)
	height := math.Round(windowHeight*scale) - float64(frame.Bottom-frame.Top)
	return int(width / scale), int(height / scale)
}

// setupPage is page.html open in a headless Chrome.
type setupPage struct {
	conn    *cdp.Conn
	address string
}

// openSetupPage opens page.html in a headless Chrome of the test's own, on a
// profile of its own with sync and sign-in off, which ends with the test.
// It is never Microsoft Edge: Edge signs a new profile in to the Windows
// account's Microsoft account and syncs it, a headless one included.
func openSetupPage(t *testing.T) setupPage {
	t.Helper()
	chrome, err := browserlogin.Executable()
	if err != nil {
		t.Fatal(err)
	}
	file, err := filepath.Abs("page.html")
	if err != nil {
		t.Fatal(err)
	}
	address := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(file)}).String()
	profile, err := os.MkdirTemp("", "setup-page-chrome-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(profile, "Default"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "Default", "Preferences"), []byte(`{"signin":{"allowed":false,"allowed_on_next_startup":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := execx.StartTree(execx.Command(chrome, "--headless", "--remote-debugging-port=0", "--user-data-dir="+profile, "--no-first-run", "--no-default-browser-check", "--disable-extensions", "--disable-sync", address))
	if err != nil {
		t.Fatal(err)
	}
	// Chrome is ended with everything it started, and gone, before its
	// profile is read and removed, which a virus scanner reading a file
	// Chrome just wrote can still hold for a moment. The profile never held
	// an account.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := tree.TerminateAndWait(ctx); err != nil {
			t.Error(err)
		}
		var preferences struct {
			AccountInfo []json.RawMessage `json:"account_info"`
		}
		if source, err := os.ReadFile(filepath.Join(profile, "Default", "Preferences")); err != nil {
			t.Error(err)
		} else if err := json.Unmarshal(source, &preferences); err != nil {
			t.Errorf("the headless Chrome's profile preferences could not be read: %v", err)
		} else if len(preferences.AccountInfo) > 0 {
			t.Error("the headless Chrome's profile was signed in to an account")
		}
		if err := fsx.RemoveAllWithin(profile, time.Minute); err != nil {
			t.Error(err)
		}
	})

	// Chrome names the port it listens on in its profile once it listens, and
	// lists the page there, beside other news, such as a service worker.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	socket := ""
	for socket == "" {
		if ctx.Err() != nil {
			t.Fatal("headless Chrome listed no page within two minutes")
		}
		time.Sleep(200 * time.Millisecond)
		active, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(string(active), "\n", 2)[0]))
		if err != nil {
			continue
		}
		var targets []struct {
			Type   string `json:"type"`
			URL    string `json:"url"`
			Socket string `json:"webSocketDebuggerUrl"`
		}
		response, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/json/list")
		if err != nil {
			continue
		}
		err = json.NewDecoder(response.Body).Decode(&targets)
		response.Body.Close()
		if err != nil {
			continue
		}
		for _, target := range targets {
			if target.Type == "page" && target.URL == address {
				socket = target.Socket
			}
		}
	}
	conn, err := cdp.Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	return setupPage{conn: conn, address: address}
}

// fits lists what does not fit the window: a part of the page that needs
// scrolling or runs past a side, a button cut by the window's edge, and an
// element the page hides that still shows. The details of a failed install
// scroll within their own box, as they are meant to.
const fits = `(() => {
  const problems = [];
  for (const hidden of document.querySelectorAll("[hidden]")) {
    if (getComputedStyle(hidden).display !== "none") problems.push("#" + hidden.id + " is hidden but shows");
  }
  for (const part of [document.scrollingElement, ...document.querySelectorAll("main, .content")]) {
    const name = part === document.scrollingElement ? "the page" : part.tagName.toLowerCase() + (part.className ? "." + part.className : "");
    if (part.scrollHeight > part.clientHeight) problems.push(name + " needs " + (part.scrollHeight - part.clientHeight) + " more pixels of height");
    if (part.scrollWidth > part.clientWidth) problems.push(name + " needs " + (part.scrollWidth - part.clientWidth) + " more pixels of width");
  }
  for (const button of document.querySelectorAll("button")) {
    if (button.getClientRects().length === 0) continue;
    const box = button.getBoundingClientRect();
    if (box.top < 0 || box.left < 0 || box.bottom > innerHeight || box.right > innerWidth) problems.push("the " + button.textContent.trim() + " button is cut by the window's edge");
  }
  return problems;
})()`

// show loads the page afresh in a width by height window at scale, runs
// script, which calls the page's functions as the setup program does, and
// returns what does not fit.
func (p setupPage) show(t *testing.T, width, height int, scale float64, script string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := p.conn.Call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": height, "deviceScaleFactor": scale, "mobile": false}, nil); err != nil {
		t.Fatal(err)
	}
	// The page shown before is marked, so the wait below is for the new one.
	p.evaluate(t, ctx, `window.shownBefore = true`)
	if err := p.conn.Call(ctx, "Page.navigate", map[string]any{"url": p.address}, nil); err != nil {
		t.Fatal(err)
	}
	for p.evaluate(t, ctx, `!window.shownBefore && document.readyState === "complete" && typeof setupPlan === "function"`) != true {
		time.Sleep(50 * time.Millisecond)
	}
	p.evaluate(t, ctx, script+"; true")
	var problems []string
	for _, problem := range p.evaluate(t, ctx, fits).([]any) {
		problems = append(problems, problem.(string))
	}
	return problems
}

// evaluate runs expression in the page and returns its value.
func (p setupPage) evaluate(t *testing.T, ctx context.Context, expression string) any {
	t.Helper()
	var result struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := p.conn.Call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Exception != nil {
		t.Fatalf("the page threw on %.80q: %s", expression, result.Exception.Text)
	}
	return result.Result.Value
}
