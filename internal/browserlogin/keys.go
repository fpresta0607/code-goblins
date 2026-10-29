package browserlogin

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/keypages"
)

const (
	// keyWait bounds the wait for the key page to load on its account, and
	// for a revealed key to show.
	keyWait = 20 * time.Second
	// signedOutWait is how long a loaded page stays off the signed-in
	// address before it is taken for the provider's sign-in page, so a page
	// still sending the browser on is not.
	signedOutWait = 2 * time.Second
	// hideWait bounds hiding the keys again, which runs even when the read
	// failed or its time ran out. It outlasts a pause of the whole machine,
	// such as a hosted runner's host takes for about ten seconds (#1048),
	// with as long again left to close the tab: a shorter bound ran out
	// inside such a pause and left the key page's tab open.
	hideWait = 30 * time.Second
	// keyPoll is how often a wait looks again.
	keyPoll = 100 * time.Millisecond
)

var (
	// ErrSignedOut is a key page that asks for a sign-in.
	ErrSignedOut = errors.New("the key page asks for a sign-in")
	// ErrPageChanged is a key page without the recipe's controls, or with
	// more than one of them.
	ErrPageChanged = errors.New("the page changed")
	// errKeyMasked is a key row whose key never showed whole.
	errKeyMasked = errors.New("the key stayed hidden or masked on the page, so nothing was read")
	// errKeyPageSlow is a key page that never settled on an address in time.
	errKeyPageSlow = errors.New("the key page did not load in time")
)

// tokensNearLabel climbs from the node a row's accessible name belongs to,
// to the nearest element that shows any word starting with the key's
// prefix, and returns those words, at most eight and each cut at 512
// characters. It returns nothing else of the page.
const tokensNearLabel = `function(prefix) {
	const words = (text) => text.split(/\s+/).filter((word) => word.startsWith(prefix));
	for (let element = this.nodeType === Node.ELEMENT_NODE ? this : this.parentElement; element; element = element.parentElement) {
		const found = words(element.innerText || "");
		for (const field of element.querySelectorAll("input, textarea")) found.push(...words(field.value));
		if (found.length > 0) return [...new Set(found)].slice(0, 8).map((word) => word.slice(0, 512));
	}
	return [];
}`

// ReadKeys reads keys from account's key page in the browser at address,
// in a tab of its own that a goblin holding the browser never drives: it
// loads the page, checks that it is signed in on that account, clicks each
// key's reveal control, reads the key from beside its row's name, and then
// loads a blank page and closes the tab, so no key is left on screen and the
// goblin's own page is as it was. It returns each value by its name, and no
// error names a value or anything else of the page.
func ReadKeys(ctx context.Context, address string, provider keypages.Provider, account string, keys []keypages.Key) (map[string]string, error) {
	page, err := provider.AccountPage(account)
	if err != nil {
		return nil, err
	}
	tab, connection, err := openTab(ctx, address)
	if err != nil {
		return nil, err
	}
	defer closeKeyTab(ctx, address, tab, connection)
	if err := openKeyPage(ctx, connection, provider, page, account); err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, key := range keys {
		if key.Reveal != "" {
			// A key is revealed only once its row is there to read it from.
			if _, err := waitNamed(ctx, connection, key.Row, ""); err != nil {
				return nil, err
			}
			if err := clickControl(ctx, connection, key.Reveal); err != nil {
				return nil, err
			}
		}
		value, err := readKey(ctx, connection, key)
		if err != nil {
			return nil, err
		}
		values[key.Name] = value
	}
	return values, nil
}

// openTab opens a blank tab in the browser at address and returns its ID
// and a connection to it.
func openTab(ctx context.Context, address string) (string, *cdp.Conn, error) {
	var created struct {
		ID string `json:"targetId"`
	}
	if err := callBrowser(ctx, address, "Target.createTarget", map[string]string{"url": "about:blank"}, &created); err != nil || created.ID == "" {
		return "", nil, errors.New("the browser did not open a tab for the key page")
	}
	tabs, err := pages(ctx, address)
	if err != nil {
		_ = closeTab(ctx, address, created.ID)
		return "", nil, err
	}
	index := slices.IndexFunc(tabs, func(tab page) bool { return tab.ID == created.ID })
	if index < 0 {
		_ = closeTab(ctx, address, created.ID)
		return "", nil, errors.New("the browser did not list the key page's tab")
	}
	connection, err := cdp.Dial(ctx, tabs[index].Socket)
	if err != nil {
		_ = closeTab(ctx, address, created.ID)
		return "", nil, errors.New("the key page did not answer")
	}
	return created.ID, connection, nil
}

// closeKeyTab loads a blank page in the key page's tab, at connection, and
// closes the tab, so no key is left on screen, even when the read failed or
// ctx ended.
func closeKeyTab(ctx context.Context, address, tab string, connection *cdp.Conn) {
	hide, cancel := context.WithTimeout(context.WithoutCancel(ctx), hideWait)
	defer cancel()
	_ = loadPage(hide, connection, "about:blank")
	connection.Close()
	_ = closeTab(hide, address, tab)
}

// closeTab closes tab id in the browser at address and returns once the
// browser no longer lists it. Chrome answers the close before the tab is
// gone, and lists it for a moment after, where it answers nothing. A close
// or a look at the list that failed, as one does when the browser stalls
// past the bridge's own bound for it, is tried again until ctx ends.
func closeTab(ctx context.Context, address, id string) error {
	closeErr := callBrowser(ctx, address, "Target.closeTarget", map[string]string{"targetId": id}, nil)
	for {
		tabs, err := pages(ctx, address)
		switch {
		case err != nil:
		case !slices.ContainsFunc(tabs, func(tab page) bool { return tab.ID == id }):
			return nil
		case closeErr != nil:
			closeErr = callBrowser(ctx, address, "Target.closeTarget", map[string]string{"targetId": id}, nil)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// callBrowser runs one command on the browser's own DevTools connection.
func callBrowser(ctx context.Context, address, method string, params, result any) error {
	socket, err := browserSocket(ctx, address)
	if err != nil {
		return err
	}
	connection, err := cdp.Dial(ctx, socket)
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Call(ctx, method, params, result)
}

// openKeyPage loads page and waits until it shows account signed in. A page
// that settles off the signed-in address is ErrSignedOut, and one signed in
// on another account is refused before anything on it is read.
func openKeyPage(caller context.Context, connection *cdp.Conn, provider keypages.Provider, page, account string) error {
	if err := loadPage(caller, connection, page); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(caller, keyWait)
	defer cancel()
	var signedOutSince time.Time
	for {
		isSignedIn, shown, err := pageSignedIn(ctx, connection, provider)
		switch {
		case errors.Is(err, errPageChanging):
			signedOutSince = time.Time{}
		case err != nil && isWaitOver(ctx, caller):
			return errKeyPageSlow
		case err != nil:
			return err
		case isSignedIn && shown == account:
			return nil
		case isSignedIn:
			return errors.New("the key page shows another account than the login's, so nothing was read")
		case signedOutSince.IsZero():
			signedOutSince = time.Now()
		case time.Since(signedOutSince) >= signedOutWait:
			return ErrSignedOut
		}
		if err := sleep(ctx, keyPoll); err != nil {
			return errKeyPageSlow
		}
	}
}

// loadPage loads address in the page and returns once the browser took it.
func loadPage(ctx context.Context, connection *cdp.Conn, address string) error {
	var answer struct {
		ErrorText string `json:"errorText"`
	}
	if err := connection.Call(ctx, "Page.navigate", map[string]string{"url": address}, &answer); err != nil {
		return errors.New("the browser did not load the key page")
	}
	if answer.ErrorText != "" {
		return errors.New("the key page did not load")
	}
	return nil
}

// axNode is a node of the page's accessibility tree, with the DOM node it
// belongs to.
type axNode struct {
	Ignored bool `json:"ignored"`
	Role    struct {
		Value string `json:"value"`
	} `json:"role"`
	Name struct {
		Value string `json:"value"`
	} `json:"name"`
	Backend int64 `json:"backendDOMNodeId"`
}

// named are the page's accessibility nodes whose accessible name is name,
// and whose role is role unless role is empty.
func named(ctx context.Context, connection *cdp.Conn, name, role string) ([]axNode, error) {
	var document struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if err := connection.Call(ctx, "Runtime.evaluate", map[string]string{"expression": "document"}, &document); err != nil || document.Result.ObjectID == "" {
		return nil, errors.New("the key page could not be read")
	}
	query := map[string]string{"objectId": document.Result.ObjectID, "accessibleName": name}
	if role != "" {
		query["role"] = role
	}
	var answer struct {
		Nodes []axNode `json:"nodes"`
	}
	if err := connection.Call(ctx, "Accessibility.queryAXTree", query, &answer); err != nil {
		return nil, errors.New("the key page's controls could not be read")
	}
	return slices.DeleteFunc(answer.Nodes, func(node axNode) bool {
		return node.Ignored || node.Backend == 0 || node.Name.Value != name || role != "" && node.Role.Value != role
	}), nil
}

// waitNamed waits for the page to show nodes named name, as named finds
// them. A page that shows none in time has changed.
func waitNamed(caller context.Context, connection *cdp.Conn, name, role string) ([]axNode, error) {
	ctx, cancel := context.WithTimeout(caller, keyWait)
	defer cancel()
	for {
		nodes, err := named(ctx, connection, name, role)
		if err != nil && isWaitOver(ctx, caller) {
			return nil, ErrPageChanged
		}
		if err != nil || len(nodes) > 0 {
			return nodes, err
		}
		if sleep(ctx, keyPoll) != nil {
			return nil, ErrPageChanged
		}
	}
}

// clickControl clicks the one button named name as a person's mouse does.
// A page with no such button, or more than one, has changed.
func clickControl(ctx context.Context, connection *cdp.Conn, name string) error {
	buttons, err := waitNamed(ctx, connection, name, "button")
	if err != nil {
		return err
	}
	if len(buttons) != 1 {
		return ErrPageChanged
	}
	target := map[string]int64{"backendNodeId": buttons[0].Backend}
	if err := connection.Call(ctx, "DOM.scrollIntoViewIfNeeded", target, nil); err != nil {
		return errors.New("the key page's control could not be reached")
	}
	var box struct {
		Quads [][]float64 `json:"quads"`
	}
	if err := connection.Call(ctx, "DOM.getContentQuads", target, &box); err != nil || len(box.Quads) == 0 || len(box.Quads[0]) != 8 {
		return errors.New("the key page's control has no place on the page")
	}
	quad := box.Quads[0]
	x, y := (quad[0]+quad[2]+quad[4]+quad[6])/4, (quad[1]+quad[3]+quad[5]+quad[7])/4
	for _, event := range []map[string]any{
		{"type": "mouseMoved", "x": x, "y": y},
		{"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1},
		{"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1},
	} {
		if err := connection.Call(ctx, "Input.dispatchMouseEvent", event, nil); err != nil {
			return errors.New("the key page's control could not be clicked")
		}
	}
	return nil
}

// readKey reads key's value from beside its row's name once the page shows
// it whole. Two different whole values beside the name are refused rather
// than one guessed at.
func readKey(caller context.Context, connection *cdp.Conn, key keypages.Key) (string, error) {
	ctx, cancel := context.WithTimeout(caller, keyWait)
	defer cancel()
	// timedOut is the read's answer once its bound ran out: the row was
	// never there, or its key never showed whole.
	labelsSeen := 0
	timedOut := func() (string, error) {
		if labelsSeen == 0 {
			return "", ErrPageChanged
		}
		return "", errKeyMasked
	}
	for {
		labels, err := named(ctx, connection, key.Row, "")
		if err != nil && isWaitOver(ctx, caller) {
			return timedOut()
		}
		if err != nil {
			return "", err
		}
		labelsSeen = len(labels)
		var whole []string
		for _, label := range labels {
			words, err := wordsNear(ctx, connection, label, key.Prefix)
			if err != nil && isWaitOver(ctx, caller) {
				return timedOut()
			}
			if err != nil {
				return "", err
			}
			for _, word := range words {
				if strings.HasPrefix(word, key.Prefix) && auth.ValueProblem(key.Name, word) == "" && !slices.Contains(whole, word) {
					whole = append(whole, word)
				}
			}
		}
		switch {
		case len(whole) == 1:
			return whole[0], nil
		case len(whole) > 1:
			return "", ErrPageChanged
		}
		if sleep(ctx, keyPoll) != nil {
			return timedOut()
		}
	}
}

// isWaitOver says a read failed because its wait's own bound ran out inside
// it while its caller's had not: on a slow machine a read of the page can
// take long enough that the bound ends during it rather than between two,
// and then what the wait found stands, not the read's own failure.
func isWaitOver(wait, caller context.Context) bool {
	return wait.Err() != nil && caller.Err() == nil
}

// wordsNear runs tokensNearLabel on label's DOM node.
func wordsNear(ctx context.Context, connection *cdp.Conn, label axNode, prefix string) ([]string, error) {
	var resolved struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if err := connection.Call(ctx, "DOM.resolveNode", map[string]int64{"backendNodeId": label.Backend}, &resolved); err != nil || resolved.Object.ObjectID == "" {
		return nil, errors.New("the key page's row could not be read")
	}
	var answer struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	err := connection.Call(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId":            resolved.Object.ObjectID,
		"functionDeclaration": tokensNearLabel,
		"arguments":           []map[string]string{{"value": prefix}},
		"returnByValue":       true,
	}, &answer)
	var words []string
	if err != nil || len(answer.Exception) != 0 || json.Unmarshal(answer.Result.Value, &words) != nil {
		return nil, errors.New("the key page's row could not be read")
	}
	return words, nil
}

// sleep waits for d, or returns ctx's error once it ends first.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
