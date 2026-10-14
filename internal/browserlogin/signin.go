package browserlogin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/keypages"
)

// accountID bounds the account a page's address names, which the recipe's
// account path already took apart.
var accountID = regexp.MustCompile(`^[A-Za-z0-9_-]{0,128}$`)

// SignedIn checks only whether a loaded page has the recipe's signed-in
// address, and which account that address names by the recipe's account
// path, "" when it names none. The browser returns those two, never its
// address, page content, password fields, codes, cookies or navigation
// history.
func SignedIn(ctx context.Context, address string, provider keypages.Provider) (bool, string, error) {
	sockets, err := pageSockets(ctx, address)
	if err != nil {
		return false, "", err
	}
	for _, socket := range sockets {
		connection, err := cdp.Dial(ctx, socket)
		if err != nil {
			return false, "", errors.New("the sign-in page did not answer")
		}
		isSignedIn, account, err := pageSignedIn(ctx, connection, provider)
		connection.Close()
		if errors.Is(err, errPageChanging) {
			continue
		}
		if err != nil {
			return false, "", err
		}
		if isSignedIn {
			return true, account, nil
		}
	}
	return false, "", nil
}

// errPageChanging is a check that met a page between two documents, which
// a later check finds settled.
var errPageChanging = errors.New("the page is loading another document")

// pageSockets are the DevTools sockets of the pages of the browser at
// address, each refused unless it is that browser's own.
func pageSockets(ctx context.Context, address string) ([]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/json/list", nil)
	if err != nil {
		return nil, errors.New("the browser has no usable address")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, errors.New("the browser did not answer")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("the browser did not list its pages")
	}
	var pages []struct {
		Type   string `json:"type"`
		Socket string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&pages); err != nil {
		return nil, errors.New("the browser gave no readable page list")
	}
	browser, err := url.Parse(address)
	if err != nil {
		return nil, errors.New("the browser has no usable address")
	}
	var sockets []string
	for _, page := range pages {
		if page.Type != "page" {
			continue
		}
		socket, err := url.Parse(page.Socket)
		if err != nil || socket.Scheme != "ws" || socket.Host != browser.Host || socket.User != nil {
			return nil, errors.New("a page is outside its browser")
		}
		sockets = append(sockets, page.Socket)
	}
	return sockets, nil
}

// pageSignedIn checks the page on connection the way SignedIn does.
func pageSignedIn(ctx context.Context, connection *cdp.Conn, provider keypages.Provider) (bool, string, error) {
	keyPage, err := url.Parse(provider.KeyPage)
	if err != nil {
		return false, "", errors.New("the provider has no usable sign-in page")
	}
	expected, err := json.Marshal(struct {
		Origin  string `json:"origin"`
		Path    string `json:"path"`
		Account string `json:"account"`
	}{keyPage.Scheme + "://" + keyPage.Host, provider.SignedInPath, provider.AccountPath})
	if err != nil {
		return false, "", err
	}
	expression := `(() => { const expected = ` + string(expected) + `; if (document.readyState !== "complete" || location.origin !== expected.origin || !new RegExp(expected.path).test(location.pathname)) return {signedIn: false, account: ""}; const match = expected.account === "" ? null : new RegExp(expected.account).exec(location.pathname); return {signedIn: true, account: match === null ? "" : match[1]} })()`
	var answer struct {
		Result struct {
			Type  string `json:"type"`
			Value struct {
				SignedIn *bool  `json:"signedIn"`
				Account  string `json:"account"`
			} `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	err = connection.Call(ctx, "Runtime.evaluate", struct {
		Expression    string `json:"expression"`
		ReturnByValue bool   `json:"returnByValue"`
	}{expression, true}, &answer)
	if err != nil {
		var refusal *cdp.Error
		if errors.As(err, &refusal) && refusal.Code == -32000 && (refusal.Message == "Execution context was destroyed." || refusal.Message == "Cannot find context with specified id") {
			return false, "", errPageChanging
		}
		return false, "", errors.New("the sign-in page could not be checked")
	}
	if len(answer.Exception) != 0 || answer.Result.Type != "object" || answer.Result.Value.SignedIn == nil || !accountID.MatchString(answer.Result.Value.Account) {
		return false, "", errors.New("the sign-in page gave no signed-in check")
	}
	return *answer.Result.Value.SignedIn, answer.Result.Value.Account, nil
}
