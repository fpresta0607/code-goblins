package supervisor

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

const (
	boardScript = `import "./chunk.js"; console.log("the board");`
	boardStyle  = `body { font-family: "Nunito"; }`
	boardFont   = "the woff2 of a font copied from public/"
)

// assetBoard serves a build as Vite writes it: a manifest naming the script,
// style and logo it named by their content, and a font it copied from
// public/assets under the font's own name.
func assetBoard(t *testing.T) *HTTP {
	t.Helper()
	store, _ := testStore(t)
	return NewHTTP(&Service{Store: store}, "board.local", fstest.MapFS{
		"index.html":                 &fstest.MapFile{Data: []byte(`<html><head></head><body><script type="module" src="/assets/index-Bx3kQ9aZ.js"></script></body></html>`)},
		".vite/manifest.json":        &fstest.MapFile{Data: []byte(`{"index.html":{"file":"assets/index-Bx3kQ9aZ.js","src":"index.html","isEntry":true,"css":["assets/index-C7m_2pLw.css"],"assets":["assets/github-D4n-8sTe.svg"]}}`)},
		"assets/index-Bx3kQ9aZ.js":   &fstest.MapFile{Data: []byte(boardScript)},
		"assets/index-C7m_2pLw.css":  &fstest.MapFile{Data: []byte(boardStyle)},
		"assets/github-D4n-8sTe.svg": &fstest.MapFile{Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		"assets/fonts/nunito.woff2":  &fstest.MapFile{Data: []byte(boardFont)},
		"manifest.webmanifest":       &fstest.MapFile{Data: []byte(`{"name":"Code Goblins"}`)},
		"push-worker.js":             &fstest.MapFile{Data: []byte(`self.addEventListener("push", () => {});`)},
	})
}

func digestOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:8])
}

func getBoard(h *HTTP, path, encodings string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("GET", "http://board.local"+path, nil)
	if encodings != "" {
		request.Header.Set("Accept-Encoding", encodings)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	return response
}

// A browser keeps a year every board file named by its content, and nothing
// else: the page, the API, the manifest, the push worker and a file copied
// from public/ asked for without the digest of its content stay no-store
// (#1966).
func TestTheBoardLetsABrowserKeepOnlyTheFilesNamedByTheirContent(t *testing.T) {
	cases := []struct {
		path, cacheControl string
	}{
		{"/assets/index-Bx3kQ9aZ.js", "private, max-age=31536000, immutable"},
		{"/assets/index-C7m_2pLw.css", "private, max-age=31536000, immutable"},
		{"/assets/github-D4n-8sTe.svg", "private, max-age=31536000, immutable"},
		{"/assets/fonts/nunito.woff2?v=" + digestOf(boardFont), "private, max-age=31536000, immutable"},
		{"/assets/fonts/nunito.woff2", "no-store"},
		{"/assets/fonts/nunito.woff2?v=" + digestOf("the font of an earlier build"), "no-store"},
		{"/", "no-store"},
		{"/api/snapshot", "no-store"},
		{"/manifest.webmanifest", "no-store"},
		{"/push-worker.js", "no-store"},
	}
	for _, test := range cases {
		t.Run(test.path, func(t *testing.T) {
			// Arrange
			h := assetBoard(t)

			// Act
			response := getBoard(h, test.path, "")

			// Assert
			if response.Code != http.StatusOK {
				t.Fatalf("GET %s = %d", test.path, response.Code)
			}
			if got := response.Header().Get("Cache-Control"); got != test.cacheControl {
				t.Fatalf("GET %s has Cache-Control %q, want %q", test.path, got, test.cacheControl)
			}
		})
	}
}

// The board's scripts and styles go gzip-compressed to a browser that takes
// gzip, as their own types, and whole to one that does not; a picture, already
// compressed, goes as it is (#1966).
func TestTheBoardSendsScriptsAndStylesGzipCompressedToABrowserThatTakesIt(t *testing.T) {
	cases := []struct {
		name, path, encodings, contentType, vary, body string
		isCompressed                                   bool
	}{
		{"a script to a browser", "/assets/index-Bx3kQ9aZ.js", "gzip, deflate, br, zstd", "text/javascript; charset=utf-8", "Accept-Encoding", boardScript, true},
		{"a style to a browser", "/assets/index-C7m_2pLw.css", "gzip, deflate, br, zstd", "text/css; charset=utf-8", "Accept-Encoding", boardStyle, true},
		{"a script asked for with a quality", "/assets/index-Bx3kQ9aZ.js", "br;q=1.0, GZIP;q=0.5", "text/javascript; charset=utf-8", "Accept-Encoding", boardScript, true},
		{"a script to a client that takes nothing", "/assets/index-Bx3kQ9aZ.js", "", "text/javascript; charset=utf-8", "Accept-Encoding", boardScript, false},
		{"a script to a client that refuses gzip", "/assets/index-Bx3kQ9aZ.js", "deflate, gzip;q=0.000", "text/javascript; charset=utf-8", "Accept-Encoding", boardScript, false},
		{"a script to a client that takes only brotli", "/assets/index-Bx3kQ9aZ.js", "br", "text/javascript; charset=utf-8", "Accept-Encoding", boardScript, false},
		{"a font", "/assets/fonts/nunito.woff2", "gzip", "", "", boardFont, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := assetBoard(t)

			// Act
			first := getBoard(h, test.path, test.encodings)
			second := getBoard(h, test.path, test.encodings)

			// Assert
			for _, response := range []*httptest.ResponseRecorder{first, second} {
				if response.Code != http.StatusOK {
					t.Fatalf("GET %s = %d", test.path, response.Code)
				}
				if got := response.Header().Get("Content-Type"); test.contentType != "" && got != test.contentType {
					t.Fatalf("GET %s is %q, want %q", test.path, got, test.contentType)
				}
				body := response.Body.String()
				if test.isCompressed {
					if response.Header().Get("Content-Encoding") != "gzip" {
						t.Fatalf("GET %s with Accept-Encoding %q came uncompressed", test.path, test.encodings)
					}
					reader, err := gzip.NewReader(response.Body)
					if err != nil {
						t.Fatal(err)
					}
					unpacked, err := io.ReadAll(reader)
					if err != nil {
						t.Fatal(err)
					}
					body = string(unpacked)
				} else if encoding := response.Header().Get("Content-Encoding"); encoding != "" {
					t.Fatalf("GET %s with Accept-Encoding %q came as %q", test.path, test.encodings, encoding)
				}
				if body != test.body {
					t.Fatalf("GET %s gave %q, want %q", test.path, body, test.body)
				}
				if vary := response.Header().Get("Vary"); vary != test.vary {
					t.Fatalf("GET %s varies by %q, want %q", test.path, vary, test.vary)
				}
			}
		})
	}
}
