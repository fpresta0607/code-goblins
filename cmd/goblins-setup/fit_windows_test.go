package main

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/fpresta0607/code-goblins/internal/priority"
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
	// The profile is named as t.TempDir names a test's folder, so one left
	// behind (removeProfile) is among the folders the janitor removes.
	profile, err := os.MkdirTemp("", "TestSetupPageChrome")
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
	// Chrome is ended with everything it started before its profile is read
	// and removed. Its job stops counting a process the moment its end
	// begins, and Windows then tears the process down on its last thread,
	// which in Chrome's browser process runs in the background, so the
	// processes are raised for their end, as browserlogin.End raises a
	// Chrome it ended. An ended job no longer lists them, so they are read
	// before it ends.
	t.Cleanup(func() {
		members, err := tree.Members()
		if err != nil {
			t.Error(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := tree.TerminateAndWait(ctx); err != nil {
			t.Error(err)
		}
		pids := make([]uint32, len(members))
		for index, member := range members {
			pids[index] = uint32(member.PID)
		}
		priority.Raise(pids)
		held, err := removeProfile(profile, 30*time.Second)
		if err != nil {
			t.Error(err)
		}
		if held != nil {
			t.Logf("the headless Chrome's profile is left for the janitor: %v", held)
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

// removeProfile reads whether the headless Chrome's profile was signed in to
// an account, and removes it, waiting out for budget whatever still holds
// one of its files. An ended Chrome holds its files until Windows has torn
// it down, which goes on after its tree has ended: a browser process torn
// down at background I/O priority held them for minutes beside other work
// on the disk (issue #2011). A profile still held after budget is left
// where it is, and held names the file still held. That is no failure,
// since by then every process the test started has been ended, and what
// holds the file is Windows finishing that end or a scanner reading it. err
// is a profile that was signed in, preferences that could not be read, or a
// removal that failed for another reason.
func removeProfile(profile string, budget time.Duration) (held, err error) {
	var preferences struct {
		AccountInfo []json.RawMessage `json:"account_info"`
	}
	if source, readErr := os.ReadFile(filepath.Join(profile, "Default", "Preferences")); readErr != nil {
		err = readErr
	} else if decodeErr := json.Unmarshal(source, &preferences); decodeErr != nil {
		err = fmt.Errorf("the headless Chrome's profile preferences could not be read: %w", decodeErr)
	} else if len(preferences.AccountInfo) > 0 {
		err = errors.New("the headless Chrome's profile was signed in to an account")
	}
	removeErr := fsx.RemoveAllWithin(profile, budget)
	// A file held open without sharing its deletion refuses it, and one a
	// process still maps, or that is already marked for deletion, denies it.
	if errors.Is(removeErr, windows.ERROR_SHARING_VIOLATION) || errors.Is(removeErr, windows.ERROR_ACCESS_DENIED) {
		return removeErr, err
	}
	return nil, errors.Join(err, removeErr)
}

// A headless Chrome's profile goes once its Chrome has ended, and a file of
// it that something still holds after that leaves the profile behind rather
// than failing the test, while a profile that was signed in to an account,
// or whose preferences cannot be read, fails it whether or not it is held.
func TestAChromeProfileStillHeldIsLeftAndASignedInOneStillFails(t *testing.T) {
	const signedOut, signedIn = `{"signin":{"allowed":false,"allowed_on_next_startup":false}}`, `{"account_info":[{"email":"someone@example.com"}]}`
	for _, test := range []struct {
		name        string
		preferences string
		isHeld      bool
		wantErr     string
	}{
		{name: "a profile that held no account", preferences: signedOut},
		{name: "a profile a file of which is still held", preferences: signedOut, isHeld: true},
		{name: "a profile signed in to an account", preferences: signedIn, wantErr: "signed in to an account"},
		{name: "a signed-in profile a file of which is still held", preferences: signedIn, isHeld: true, wantErr: "signed in to an account"},
		{name: "a profile whose preferences cannot be read", preferences: `{"account_info":`, wantErr: "could not be read"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			profile := filepath.Join(t.TempDir(), "profile")
			if err := os.MkdirAll(filepath.Join(profile, "Default"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(profile, "Default", "Preferences"), []byte(test.preferences), 0o644); err != nil {
				t.Fatal(err)
			}
			if test.isHeld {
				// Go opens a file without sharing its deletion, as Chrome
				// opens this one.
				holder, err := os.Create(filepath.Join(profile, "CrashpadMetrics-active.pma"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { holder.Close() })
			}

			// Act
			held, err := removeProfile(profile, 100*time.Millisecond)

			// Assert
			if test.wantErr == "" && err != nil || test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Errorf("err = %v, want %q", err, test.wantErr)
			}
			_, statErr := os.Stat(profile)
			if isLeft := statErr == nil; isLeft != test.isHeld || (held != nil) != test.isHeld {
				t.Errorf("left = %v, held = %v; want the profile left, and its holder said, only while a file of it is held", isLeft, held)
			}
		})
	}
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
