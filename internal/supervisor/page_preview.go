package supervisor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// maxPagePreview caps one page's export, which carries its pictures and a
// delivered PDF inline.
const maxPagePreview = 48 << 20

// pagePreviewTimeout bounds one export of a page for its card.
const pagePreviewTimeout = 15 * time.Second

// pageSessionKey is the key of a review session in its Scrawl link,
// /session/<key>.
var pageSessionKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// pagePreviewPolicy sandboxes a page's preview in an origin of its own: its
// scripts run, as they do on the page, and reach nothing of the board's, and
// only the board's card may frame it.
const pagePreviewPolicy = "sandbox allow-scripts; frame-ancestors 'self'"

// pagePreviewClient fetches an export from the review server a page's link
// names and follows no redirect elsewhere.
var pagePreviewClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// reviewPreview serves item id's page at /api/reviews/<id>/preview for its
// card to show the page itself: lavish-axi's export of it, its own files
// inline, from the review server its link names. An export reads the page and
// opens no review window, so a preview never stands for the Overlord's look.
func (h *HTTP) reviewPreview(w http.ResponseWriter, r *http.Request, id string) {
	reviews := h.Service.Store.Snapshot().Reviews
	i := slices.IndexFunc(reviews, func(item Review) bool { return item.ID == id && item.LavishPage != "" })
	if i < 0 {
		apiError(w, 404, "Unknown page")
		return
	}
	export, err := pageExportURL(reviews[i].Lavish)
	if err != nil {
		apiError(w, 404, "Unknown page")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), pagePreviewTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, export, nil)
	if err != nil {
		apiError(w, 500, "The page cannot be shown")
		return
	}
	response, err := pagePreviewClient.Do(request)
	if err != nil {
		apiError(w, 502, "The page cannot be shown")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		apiError(w, 502, "The page cannot be shown")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", pagePreviewPolicy)
	_, _ = io.Copy(w, io.LimitReader(response.Body, maxPagePreview))
}

// pageExportURL is where the review server that serves the Scrawl page at
// link exports it: /api/<key>/export beside its /session/<key>.
func pageExportURL(link string) (string, error) {
	page, err := url.Parse(link)
	if err != nil || (page.Scheme != "http" && page.Scheme != "https") || page.Host == "" || page.User != nil {
		return "", errors.New("not a review page's link")
	}
	key, ok := strings.CutPrefix(page.Path, "/session/")
	if !ok || !pageSessionKey.MatchString(key) {
		return "", errors.New("not a review page's link")
	}
	return (&url.URL{Scheme: page.Scheme, Host: page.Host, Path: "/api/" + key + "/export"}).String(), nil
}
