package supervisor

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fpresta0607/code-goblins/internal/boardweb"
)

const (
	boardScript = `import "./chunk.js"; console.log("the board");`
	boardStyle  = `body { font-family: "Nunito"; }`
	boardFont   = "the woff2 of a font copied from public/"
	// boardFontDigest is the first 16 hex digits of the SHA-256 of
	// boardFont, and earlierFontDigest those of "the font of an earlier
	// build", as sha256sum gives them.
	boardFontDigest   = "fcc91b28ee7b14da"
	earlierFontDigest = "c71a2a00af17881a"
	keptAYear         = "private, max-age=31536000, immutable"
)

// builtAddress is an address under /assets/ as a build writes it into its
// page, a style or a script: opened by a quote or a parenthesis, and closed
// by the next one.
var builtAddress = regexp.MustCompile("[\"'`(](/assets/[^\"'`()\\\\\\s]+)")

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
		{"/assets/index-Bx3kQ9aZ.js", keptAYear},
		{"/assets/index-C7m_2pLw.css", keptAYear},
		{"/assets/github-D4n-8sTe.svg", keptAYear},
		{"/assets/fonts/nunito.woff2?v=" + boardFontDigest, keptAYear},
		{"/assets/fonts/nunito.woff2", "no-store"},
		{"/assets/fonts/nunito.woff2?v=" + earlierFontDigest, "no-store"},
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
// gzip, as their own types, and whole to one that does not, kept a year
// either way; a picture, already compressed, goes as it is (#1966).
func TestTheBoardSendsScriptsAndStylesGzipCompressedToABrowserThatTakesIt(t *testing.T) {
	// Arrange: this Windows names JavaScript's and CSS's types text/plain,
	// as some registries do.
	for _, extension := range []string{".js", ".css"} {
		registered := mime.TypeByExtension(extension)
		if err := mime.AddExtensionType(extension, "text/plain"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = mime.AddExtensionType(extension, registered) })
	}
	cases := []struct {
		name, path, encodings, contentType, cacheControl, vary, body string
		isCompressed                                                 bool
	}{
		{"a script to a browser", "/assets/index-Bx3kQ9aZ.js", "gzip, deflate, br, zstd", "text/javascript; charset=utf-8", keptAYear, "Accept-Encoding", boardScript, true},
		{"a style to a browser", "/assets/index-C7m_2pLw.css", "gzip, deflate, br, zstd", "text/css; charset=utf-8", keptAYear, "Accept-Encoding", boardStyle, true},
		{"a script asked for with a quality", "/assets/index-Bx3kQ9aZ.js", "br;q=1.0, GZIP;q=0.5", "text/javascript; charset=utf-8", keptAYear, "Accept-Encoding", boardScript, true},
		{"a script to a client that takes nothing", "/assets/index-Bx3kQ9aZ.js", "", "text/javascript; charset=utf-8", keptAYear, "Accept-Encoding", boardScript, false},
		{"a style to a client that takes nothing", "/assets/index-C7m_2pLw.css", "", "text/css; charset=utf-8", keptAYear, "Accept-Encoding", boardStyle, false},
		{"a script to a client that refuses gzip", "/assets/index-Bx3kQ9aZ.js", "deflate, gzip;q=0.000", "text/javascript; charset=utf-8", keptAYear, "Accept-Encoding", boardScript, false},
		{"a script to a client that takes only brotli", "/assets/index-Bx3kQ9aZ.js", "br", "text/javascript; charset=utf-8", keptAYear, "Accept-Encoding", boardScript, false},
		{"a font", "/assets/fonts/nunito.woff2", "gzip", "", "no-store", "", boardFont, false},
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
				if got := response.Header().Get("Cache-Control"); got != test.cacheControl {
					t.Fatalf("GET %s with Accept-Encoding %q has Cache-Control %q, want %q", test.path, test.encodings, got, test.cacheControl)
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

// A page of an earlier build that imports a script this build no longer has
// is answered not found, as a browser that takes gzip asks for it, so the
// import fails and the board reloads onto this build (#1966).
func TestTheBoardAnswersAScriptOfAnEarlierBuildNotFound(t *testing.T) {
	// Arrange
	h := assetBoard(t)

	// Act
	response := getBoard(h, "/assets/TerminalDeck-OLD.js", "gzip, deflate, br, zstd")

	// Assert
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /assets/TerminalDeck-OLD.js = %d, want 404", response.Code)
	}
	if encoding := response.Header().Get("Content-Encoding"); encoding != "" {
		t.Fatalf("GET /assets/TerminalDeck-OLD.js came as %q, want no encoding", encoding)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("GET /assets/TerminalDeck-OLD.js has Cache-Control %q, want no-store", got)
	}
}

// The board as its build wrote it, embedded as goblins.exe embeds it: a
// browser keeps a year every file the build's manifest lists and every
// address under /assets/ the build wrote into its page, styles and scripts,
// asked for as written, so the manifest Vite writes and the digests
// vite.config.ts writes are the ones the supervisor reads (#1966). CI's
// frontend job builds the board and runs this test; where the board is not
// built it skips.
func TestTheBuiltBoardLetsABrowserKeepEveryFileItAddresses(t *testing.T) {
	// Arrange
	if !boardweb.Built() {
		t.Skip("the board is not built: npm ci and npm run build in frontend build it")
	}
	assets, err := boardweb.Assets()
	if err != nil {
		t.Fatal(err)
	}
	h := NewHTTP(&Service{}, "board.local", assets)
	data, err := fs.ReadFile(assets, ".vite/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("the build's manifest is not Vite's map of chunks: %v", err)
	}
	var listed []string
	for _, chunk := range manifest {
		for _, value := range chunk {
			values, isList := value.([]any)
			if !isList {
				values = []any{value}
			}
			for _, item := range values {
				if file, isFile := item.(string); isFile && strings.HasPrefix(file, "assets/") {
					listed = append(listed, file)
				}
			}
		}
	}
	var addresses []string
	versioned := 0
	for _, file := range append([]string{"index.html"}, listed...) {
		if file != "index.html" && path.Ext(file) != ".js" && path.Ext(file) != ".css" {
			continue
		}
		content, err := fs.ReadFile(assets, file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range builtAddress.FindAllStringSubmatch(string(content), -1) {
			addresses = append(addresses, match[1])
			if strings.Contains(match[1], "?v=") {
				versioned++
			}
		}
	}
	if len(listed) == 0 || versioned == 0 {
		t.Fatalf("the build's manifest lists %d files under assets/ and its page, styles and scripts write %d versioned addresses, want some of each", len(listed), versioned)
	}
	for _, file := range listed {
		addresses = append(addresses, "/"+file)
	}
	slices.Sort(addresses)
	addresses = slices.Compact(addresses)

	// Act
	responses := map[string]*httptest.ResponseRecorder{}
	for _, address := range addresses {
		responses[address] = getBoard(h, address, "gzip, deflate, br, zstd")
	}

	// Assert
	for address, response := range responses {
		if got := response.Header().Get("Cache-Control"); response.Code != http.StatusOK || got != keptAYear {
			t.Errorf("GET %s = %d with Cache-Control %q, want 200 and %q", address, response.Code, got, keptAYear)
		}
	}
}
