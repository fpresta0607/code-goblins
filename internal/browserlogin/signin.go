package browserlogin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/fpresta0607/code-goblins/internal/cdp"
	"github.com/fpresta0607/code-goblins/internal/keypages"
)

// SignedIn checks only whether a loaded page has the recipe's signed-in
// address. The browser returns a boolean, never its address, page content,
// password fields, codes, cookies or navigation history.
func SignedIn(ctx context.Context, address string, provider keypages.Provider) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/json/list", nil)
	if err != nil {
		return false, errors.New("the sign-in browser has no usable address")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false, errors.New("the sign-in browser did not answer")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, errors.New("the sign-in browser did not list its pages")
	}
	var pages []struct {
		Type   string `json:"type"`
		Socket string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&pages); err != nil {
		return false, errors.New("the sign-in browser gave no readable page list")
	}
	keyPage, err := url.Parse(provider.KeyPage)
	if err != nil {
		return false, errors.New("the provider has no usable sign-in page")
	}
	expected, err := json.Marshal(struct {
		Origin string `json:"origin"`
		Path   string `json:"path"`
	}{keyPage.Scheme + "://" + keyPage.Host, provider.SignedInPath})
	if err != nil {
		return false, err
	}
	expression := `(() => { const expected = ` + string(expected) + `; return document.readyState === "complete" && location.origin === expected.origin && new RegExp(expected.path).test(location.pathname) })()`
	browser, err := url.Parse(address)
	if err != nil {
		return false, errors.New("the sign-in browser has no usable address")
	}
	for _, page := range pages {
		if page.Type != "page" {
			continue
		}
		socket, err := url.Parse(page.Socket)
		if err != nil || socket.Scheme != "ws" || socket.Host != browser.Host || socket.User != nil {
			return false, errors.New("the sign-in page is outside its browser")
		}
		connection, err := cdp.Dial(ctx, page.Socket)
		if err != nil {
			return false, errors.New("the sign-in page did not answer")
		}
		var answer struct {
			Result struct {
				Type  string `json:"type"`
				Value bool   `json:"value"`
			} `json:"result"`
			Exception json.RawMessage `json:"exceptionDetails"`
		}
		err = connection.Call(ctx, "Runtime.evaluate", struct {
			Expression    string `json:"expression"`
			ReturnByValue bool   `json:"returnByValue"`
		}{expression, true}, &answer)
		connection.Close()
		if err != nil {
			var refusal *cdp.Error
			if errors.As(err, &refusal) && refusal.Code == -32000 && (refusal.Message == "Execution context was destroyed." || refusal.Message == "Cannot find context with specified id") {
				continue
			}
			return false, errors.New("the sign-in page could not be checked")
		}
		if len(answer.Exception) != 0 || answer.Result.Type != "boolean" {
			return false, errors.New("the sign-in page gave no signed-in check")
		}
		if answer.Result.Value {
			return true, nil
		}
	}
	return false, nil
}
