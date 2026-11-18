// Package pagecheck checks a Scrawl page the way the Overlord's review window
// does, with no browser of the goblin's own: a hidden headless Chrome loads
// the page's review session at a desktop and a phone width, so lavish-axi's
// own layout gate measures it there, saves a picture of the page at each,
// and closes. The findings are lavish-axi's own, read back from its server,
// so a goblin sees what his Layout issues inbox would show (#2542).
package pagecheck

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// View is one window size the page is checked and pictured at.
type View struct {
	Name          string
	Width, Height int
	IsMobile      bool
}

// Views are the sizes a page is checked at: his desktop's window and a
// phone's.
var Views = []View{{Name: "desktop", Width: 1920, Height: 1080}, {Name: "phone", Width: 390, Height: 844, IsMobile: true}}

// Warning is one layout issue lavish-axi's gate found, as its inbox shows it.
type Warning struct {
	Rule          string `json:"rule"`
	Title         string `json:"title"`
	Explanation   string `json:"explanation"`
	Selector      string `json:"selector"`
	ViewportClass string `json:"viewport_class"`
	Status        string `json:"status"`
}

// Picture is the page as drawn at one view, saved as a PNG.
type Picture struct {
	View View
	Path string
}

// Report is what a check found: a picture at each view and every active
// layout warning.
type Report struct {
	Pictures []Picture
	Warnings []Warning
}

// activeStatuses are the warning statuses lavish-axi counts as open in the
// Overlord's inbox; resolved, dismissed and obsolete ones are not.
var activeStatuses = []string{"open", "queued", "recurring", "unverified", "reopened"}

// longestPicture caps a picture's height, which follows the page's.
const longestPicture = 16384

var (
	// settleTimeout bounds the wait for the gate's first report at a view.
	settleTimeout = 30 * time.Second
	// settleQuiet is how long the gate's revision stays put before its
	// report counts as settled: it measures twice before it records.
	settleQuiet = 2 * time.Second
	pollEvery   = 250 * time.Millisecond
)

// Check checks the page whose review session is at session, a lavish-axi
// link of the form http://<host>/session/<key>, in a hidden Chrome started
// from executable, and saves its pictures in out.
func Check(ctx context.Context, executable, session, out string) (report Report, failure error) {
	link, err := url.Parse(session)
	if err != nil || link.Scheme != "http" || !strings.HasPrefix(link.Path, "/session/") || strings.Trim(strings.TrimPrefix(link.Path, "/session/"), "/") == "" {
		return Report{}, fmt.Errorf("%s is not a Scrawl session link", session)
	}
	key := strings.Trim(strings.TrimPrefix(link.Path, "/session/"), "/")
	origin := link.Scheme + "://" + link.Host
	warnings := origin + "/api/" + key + "/layout-warnings"
	if _, err := readWarnings(ctx, warnings); err != nil {
		return Report{}, err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return Report{}, err
	}
	profile, err := os.MkdirTemp("", "goblins-page-check-")
	if err != nil {
		return Report{}, err
	}
	process, err := browserlogin.Start(ctx, browserlogin.Launch{Executable: executable, Profile: profile, Headless: true, Hosts: []string{link.Hostname()}})
	defer func() {
		if process.PID != 0 {
			if err := browserlogin.End(context.WithoutCancel(ctx), process); err != nil {
				failure = errors.Join(failure, fmt.Errorf("close the hidden Chrome %d: %w", process.PID, err))
				return
			}
		}
		failure = errors.Join(failure, fsx.RemoveAll(profile))
	}()
	if err != nil {
		return Report{}, fmt.Errorf("start a hidden Chrome: %w", err)
	}
	client, err := process.Open(ctx)
	if err != nil {
		return Report{}, err
	}
	defer client.Close()
	for _, view := range Views {
		path := filepath.Join(out, view.Name+".png")
		if err := checkView(ctx, client, view, session, origin+"/artifact/"+key+"/", warnings, path); err != nil {
			return Report{}, fmt.Errorf("at the %s width: %w", view.Name, err)
		}
		report.Pictures = append(report.Pictures, Picture{View: view, Path: path})
	}
	found, err := readWarnings(ctx, warnings)
	if err != nil {
		return Report{}, err
	}
	for _, warning := range found.Warnings {
		if slices.Contains(activeStatuses, warning.Status) {
			report.Warnings = append(report.Warnings, warning)
		}
	}
	return report, nil
}

// checkView loads the session at view in a page of its own, waits for the
// gate to record what it measured, then saves the page's frame, grown to
// the page's whole height, as a picture at path.
func checkView(ctx context.Context, client *browserlogin.Client, view View, session, frame, warnings, path string) error {
	var created struct {
		Target string `json:"targetId"`
	}
	if err := client.Call(ctx, "", "Target.createTarget", map[string]string{"url": "about:blank"}, &created); err != nil {
		return err
	}
	defer client.Call(context.WithoutCancel(ctx), "", "Target.closeTarget", map[string]string{"targetId": created.Target}, nil)
	page, err := attach(ctx, client, created.Target)
	if err != nil {
		return err
	}
	if err := resize(ctx, client, page, view, view.Height); err != nil {
		return err
	}
	before, err := readWarnings(ctx, warnings)
	if err != nil {
		return err
	}
	if err := client.Call(ctx, page, "Page.navigate", map[string]string{"url": session}, nil); err != nil {
		return err
	}
	if err := settle(ctx, warnings, before.Revision); err != nil {
		return err
	}
	content, err := frameHeight(ctx, client, frame)
	if err != nil {
		return err
	}
	shown, err := frameBox(ctx, client, page)
	if err != nil {
		return err
	}
	if grown := min(view.Height+content-int(shown.Height), longestPicture); grown > view.Height {
		if err := resize(ctx, client, page, view, grown); err != nil {
			return err
		}
		for deadline := time.Now().Add(3 * time.Second); int(shown.Height) < content && time.Now().Before(deadline); {
			if err := wait(ctx, pollEvery); err != nil {
				return err
			}
			if shown, err = frameBox(ctx, client, page); err != nil {
				return err
			}
		}
	}
	var picture struct {
		Data string `json:"data"`
	}
	clip := map[string]float64{"x": shown.X, "y": shown.Y, "width": shown.Width, "height": shown.Height, "scale": 1}
	if err := client.Call(ctx, page, "Page.captureScreenshot", map[string]any{"format": "png", "clip": clip}, &picture); err != nil {
		return err
	}
	data, err := base64.StdEncoding.DecodeString(picture.Data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func attach(ctx context.Context, client *browserlogin.Client, target string) (string, error) {
	var attached struct {
		Session string `json:"sessionId"`
	}
	err := client.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true}, &attached)
	return attached.Session, err
}

func resize(ctx context.Context, client *browserlogin.Client, page string, view View, height int) error {
	return client.Call(ctx, page, "Emulation.setDeviceMetricsOverride", map[string]any{"width": view.Width, "height": height, "deviceScaleFactor": 1, "mobile": view.IsMobile}, nil)
}

// box is where the page's frame sits in the review window.
type box struct {
	X, Y, Width, Height float64
}

func frameBox(ctx context.Context, client *browserlogin.Client, page string) (box, error) {
	var found box
	err := evaluate(ctx, client, page, `(() => { const r = document.querySelector('iframe[src*="/artifact/"]')?.getBoundingClientRect(); return r ? { X: r.x, Y: r.y, Width: r.width, Height: r.height } : null; })()`, &found)
	if err == nil && (found.Width == 0 || found.Height == 0) {
		err = errors.New("the review window shows no page")
	}
	return found, err
}

// frameHeight is the height of the page itself, read in its frame, which
// Chrome runs as a target of its own since the frame is sandboxed. A frame
// Chrome kept in the review window's own process has no target, so its
// height is 0 and its picture is the window's height.
func frameHeight(ctx context.Context, client *browserlogin.Client, frame string) (int, error) {
	var targets struct {
		Infos []struct {
			Target string `json:"targetId"`
			Type   string `json:"type"`
			URL    string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := client.Call(ctx, "", "Target.getTargets", nil, &targets); err != nil {
		return 0, err
	}
	for _, info := range targets.Infos {
		if info.Type != "iframe" || !strings.HasPrefix(info.URL, frame) {
			continue
		}
		session, err := attach(ctx, client, info.Target)
		if err != nil {
			return 0, err
		}
		defer client.Call(context.WithoutCancel(ctx), "", "Target.detachFromTarget", map[string]string{"sessionId": session}, nil)
		var height int
		err = evaluate(ctx, client, session, `Math.ceil(Math.max(document.documentElement.scrollHeight, document.body ? document.body.scrollHeight : 0))`, &height)
		return height, err
	}
	return 0, nil
}

func evaluate(ctx context.Context, client *browserlogin.Client, session, expression string, value any) error {
	var evaluated struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := client.Call(ctx, session, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true}, &evaluated); err != nil {
		return err
	}
	if evaluated.Exception != nil {
		return errors.New(evaluated.Exception.Text)
	}
	return json.Unmarshal(evaluated.Result.Value, value)
}

// recorded is lavish-axi's answer for a session's layout warnings.
type recorded struct {
	Warnings []Warning `json:"warnings"`
	Revision int       `json:"revision"`
}

func readWarnings(ctx context.Context, address string) (recorded, error) {
	asked, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(asked, http.MethodGet, address, nil)
	if err != nil {
		return recorded{}, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return recorded{}, fmt.Errorf("lavish-axi's server did not answer: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return recorded{}, fmt.Errorf("lavish-axi's server answered %s for the page's layout warnings", response.Status)
	}
	var found recorded
	if err := json.NewDecoder(response.Body).Decode(&found); err != nil {
		return recorded{}, fmt.Errorf("lavish-axi's layout warnings could not be read: %w", err)
	}
	return found, nil
}

// settle waits until the gate has recorded past revision and then recorded
// nothing new for settleQuiet, or until settleTimeout has passed since it
// first recorded.
func settle(ctx context.Context, warnings string, revision int) error {
	deadline := time.Now().Add(settleTimeout)
	last, still := revision, time.Time{}
	for {
		found, err := readWarnings(ctx, warnings)
		if err != nil {
			return err
		}
		switch {
		case found.Revision != last:
			last, still = found.Revision, time.Now()
		case last != revision && time.Since(still) >= settleQuiet:
			return nil
		}
		if time.Now().After(deadline) {
			if last == revision {
				return fmt.Errorf("lavish-axi's layout gate reported nothing within %s", settleTimeout)
			}
			return nil
		}
		if err := wait(ctx, pollEvery); err != nil {
			return err
		}
	}
}

func wait(ctx context.Context, pause time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(pause):
		return nil
	}
}
