package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubRelease is a release source as GitHub serves one: the latest release's
// record at /latest, with an ETag, and each asset under /download/.
type stubRelease struct {
	mu       sync.Mutex
	release  map[string]any
	assets   map[string][]byte
	etag     string
	full     int
	notMod   int
	server   *httptest.Server
	statuses map[string]int
}

func newStubRelease(t *testing.T, tag string, assets map[string][]byte) *stubRelease {
	t.Helper()
	stub := &stubRelease{assets: assets, etag: `"v1"`, statuses: map[string]int{}}
	stub.server = httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(stub.server.Close)
	stub.release = map[string]any{
		"tag_name":     tag,
		"html_url":     stub.server.URL + "/tag/" + tag,
		"body":         "**This release is unsigned.**\n\n## What's Changed\n* feat(board): an update arrives in the Command Center by @fpresta0607 in https://github.com/fpresta0607/code-goblins/pull/396\n* fix(install): keep a home in use by @fpresta0607 in https://github.com/fpresta0607/code-goblins/pull/391\n\n**Full Changelog**: https://github.com/fpresta0607/code-goblins/compare/v0.4.0...v0.5.0\n",
		"published_at": "2026-10-06T14:02:00Z",
		"draft":        false,
		"prerelease":   false,
		"assets":       stub.assetList(),
	}
	return stub
}

func (s *stubRelease) assetList() []map[string]string {
	var list []map[string]string
	for name := range s.assets {
		list = append(list, map[string]string{"name": name, "browser_download_url": s.server.URL + "/download/" + name})
	}
	return list
}

func (s *stubRelease) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status := s.statuses[r.URL.Path]; status != 0 {
		w.WriteHeader(status)
		return
	}
	if r.URL.Path == "/latest" {
		if r.Header.Get("If-None-Match") == s.etag {
			s.notMod++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		s.full++
		w.Header().Set("ETag", s.etag)
		_ = json.NewEncoder(w).Encode(s.release)
		return
	}
	if name, ok := strings.CutPrefix(r.URL.Path, "/download/"); ok {
		if data, found := s.assets[name]; found {
			_, _ = w.Write(data)
			return
		}
	}
	http.NotFound(w, r)
}

func (s *stubRelease) source() Source {
	return Source{API: s.server.URL + "/latest", Site: s.server.URL + "/", Downloads: s.server.URL + "/"}
}

func sum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// releaseAssets are a release's files: its programs, their sums as the
// release workflow writes them, and its install script, naming publisher.
func releaseAssets(publisher string, programs map[string][]byte) map[string][]byte {
	assets := map[string][]byte{}
	var lines []string
	for name, data := range programs {
		assets[name] = data
		lines = append(lines, sum(data)+"  "+name)
	}
	assets["SHA256SUMS"] = []byte(strings.Join(lines, "\n") + "\n")
	assets["install.ps1"] = []byte("& {\n    $repo = \"fpresta0607/code-goblins\"\n    $releasePublisher = \"" + publisher + "\"\n}\n")
	return assets
}

func TestStandingComparesThisBuildWithTheRelease(t *testing.T) {
	cases := []struct {
		installed, latest string
		want              Standing
	}{
		{"v0.4.2", "v0.5.0", Available},
		{"v0.5.0", "v0.5.0", UpToDate},
		{"v0.6.0", "v0.5.0", Ahead},
		{"v0.9.0", "v0.10.0", Available},
		{"v1.0.0", "v0.10.9", Ahead},
		{"dev", "v0.5.0", FromSource},
		{"", "v0.5.0", FromSource},
		{"v0.5.0-rc1", "v0.5.0", FromSource},
	}
	for _, c := range cases {
		t.Run(c.installed+" against "+c.latest, func(t *testing.T) {
			if got := StandingOf(c.installed, c.latest); got != c.want {
				t.Fatalf("StandingOf(%q, %q) = %q, want %q", c.installed, c.latest, got, c.want)
			}
		})
	}
}

func TestLatestReadsTheReleaseAndAsksAgainWithItsETag(t *testing.T) {
	stub := newStubRelease(t, "v0.5.0", releaseAssets("", map[string][]byte{"cfo.exe": []byte("new build")}))
	state := t.TempDir()

	first, err := Latest(context.Background(), http.DefaultClient, stub.source(), state)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Latest(context.Background(), http.DefaultClient, stub.source(), state)
	if err != nil {
		t.Fatal(err)
	}

	if first.Tag != "v0.5.0" || second.Tag != "v0.5.0" || !first.Published.Equal(time.Date(2026, 10, 6, 14, 2, 0, 0, time.UTC)) {
		t.Fatalf("got %+v then %+v, want v0.5.0 published 2026-10-06 14:02 both times", first, second)
	}
	if stub.full != 1 || stub.notMod != 1 {
		t.Fatalf("the source sent %d full answers and %d not-modified, want 1 and 1: the second ask carries the kept ETag", stub.full, stub.notMod)
	}
	kept, fetched, ok := Cached(state, stub.source())
	if !ok || kept.Tag != "v0.5.0" || fetched.IsZero() {
		t.Fatalf("Cached = %+v, %v, %v; want the release kept with when it was read", kept, fetched, ok)
	}
}

func TestLatestOfflineFailsAndKeepsWhatItKnew(t *testing.T) {
	stub := newStubRelease(t, "v0.5.0", releaseAssets("", map[string][]byte{"cfo.exe": []byte("new build")}))
	state := t.TempDir()
	if _, err := Latest(context.Background(), http.DefaultClient, stub.source(), state); err != nil {
		t.Fatal(err)
	}
	stub.server.Close()

	_, err := Latest(context.Background(), http.DefaultClient, stub.source(), state)

	if err == nil {
		t.Fatal("Latest succeeded with the release source unreachable")
	}
	if kept, _, ok := Cached(state, stub.source()); !ok || kept.Tag != "v0.5.0" {
		t.Fatalf("Cached = %+v, %v after a failed ask; want the release read before", kept, ok)
	}
}

func TestLatestRefusesAReleaseItCannotOffer(t *testing.T) {
	cases := map[string]func(map[string]any){
		"a draft":                  func(r map[string]any) { r["draft"] = true },
		"a prerelease":             func(r map[string]any) { r["prerelease"] = true },
		"a tag that is no version": func(r map[string]any) { r["tag_name"] = "nightly" },
		"a page on another site":   func(r map[string]any) { r["html_url"] = "javascript:alert(1)" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			stub := newStubRelease(t, "v0.5.0", releaseAssets("", map[string][]byte{"cfo.exe": []byte("new build")}))
			change(stub.release)

			_, err := Latest(context.Background(), http.DefaultClient, stub.source(), t.TempDir())

			if err == nil {
				t.Fatalf("Latest offered %s", name)
			}
		})
	}
}

func TestLatestSaysWhatTheSourceAnswered(t *testing.T) {
	stub := newStubRelease(t, "v0.5.0", releaseAssets("", map[string][]byte{"cfo.exe": []byte("new build")}))
	stub.statuses["/latest"] = http.StatusForbidden

	_, err := Latest(context.Background(), http.DefaultClient, stub.source(), t.TempDir())

	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("Latest = %v, want the HTTP 403 named", err)
	}
}

func fetchFrom(t *testing.T, stub *stubRelease, signedBy SignatureReader) (Download, string, error) {
	t.Helper()
	state := t.TempDir()
	release, err := Latest(context.Background(), http.DefaultClient, stub.source(), state)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state, "update", "release", release.Tag)
	download, err := Fetch(context.Background(), http.DefaultClient, stub.source(), release, dir, signedBy, nil)
	return download, dir, err
}

func noSignature(t *testing.T) SignatureReader {
	return func(context.Context, string) (string, string, error) {
		t.Error("an unsigned release's download had its signature read")
		return "", "", errors.New("not expected")
	}
}

func TestFetchReportsCheckingAfterAllProgramsDownload(t *testing.T) {
	cases := []struct {
		name            string
		isBadChecksum   bool
		isDownloadError bool
		status          string
		wantError       string
		wantSignatures  int
	}{
		{name: "verified", status: "Valid", wantSignatures: 2},
		{name: "checksum refused", isBadChecksum: true, status: "Valid", wantError: "does not match the release's SHA256SUMS"},
		{name: "signature refused", status: "HashMismatch", wantError: "not validly signed", wantSignatures: 1},
		{name: "download failed", isDownloadError: true, status: "Valid", wantError: "HTTP 502"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assets := releaseAssets("SIQstack LLC", map[string][]byte{Program: []byte("new build"), Window: []byte("new window")})
			if test.isBadChecksum {
				assets[Program] = []byte("tampered build")
			}
			stub := newStubRelease(t, "v0.5.0", assets)
			if test.isDownloadError {
				stub.statuses["/download/"+Window] = http.StatusBadGateway
			}
			state := t.TempDir()
			latest, err := Latest(context.Background(), http.DefaultClient, stub.source(), state)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(state, "release")
			checks, signatures := 0, 0
			checking := func() {
				checks++
				if signatures != 0 {
					t.Error("signature checks began before checking was reported")
				}
				for _, name := range []string{Program, Window} {
					data, err := os.ReadFile(filepath.Join(dir, name+".download"))
					if err != nil || string(data) != string(assets[name]) {
						t.Errorf("checking began before %s finished downloading: %q, %v", name, data, err)
					}
					if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("%s was kept before checking began: %v", name, err)
					}
				}
			}
			signedBy := func(context.Context, string) (string, string, error) {
				if checks != 1 {
					t.Error("a signature was read before checking was reported once")
				}
				signatures++
				return test.status, "SIQstack LLC", nil
			}

			_, err = Fetch(context.Background(), http.DefaultClient, stub.source(), latest, dir, signedBy, checking)

			if test.wantError == "" && err != nil || test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("Fetch = %v, want error %q", err, test.wantError)
			}
			wantChecks := 1
			if test.isDownloadError {
				wantChecks = 0
			}
			if checks != wantChecks || signatures != test.wantSignatures {
				t.Fatalf("checking reported %d times, signatures read %d times; want %d and %d", checks, signatures, wantChecks, test.wantSignatures)
			}
			if test.wantError != "" {
				if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("a refused download was kept: %v", err)
				}
			}
		})
	}
}

func TestFetchKeepsTheProgramsThatMatchTheReleaseSums(t *testing.T) {
	programs := map[string][]byte{"cfo.exe": []byte("new build"), "goblins-window.exe": []byte("new window")}
	stub := newStubRelease(t, "v0.5.0", releaseAssets("", programs))

	download, dir, err := fetchFrom(t, stub, noSignature(t))

	if err != nil {
		t.Fatal(err)
	}
	if download.Program != filepath.Join(dir, "cfo.exe") || download.Window != filepath.Join(dir, "goblins-window.exe") || download.Publisher != "" {
		t.Fatalf("download = %+v, want cfo.exe and the window in %s, unsigned", download, dir)
	}
	for name, want := range programs {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s holds %q (%v), want %q", name, got, err, want)
		}
	}
	if download.Sums["cfo.exe"] != sum(programs["cfo.exe"]) {
		t.Fatalf("download.Sums = %v, want cfo.exe's SHA-256", download.Sums)
	}
}

func TestFetchWithoutAWindowKeepsTheProgramAlone(t *testing.T) {
	stub := newStubRelease(t, "v0.5.0", releaseAssets("", map[string][]byte{"cfo.exe": []byte("new build")}))

	download, _, err := fetchFrom(t, stub, noSignature(t))

	if err != nil || download.Program == "" || download.Window != "" {
		t.Fatalf("download = %+v, %v; want cfo.exe alone", download, err)
	}
}

func TestFetchRefusesAProgramThatDoesNotMatchItsSum(t *testing.T) {
	assets := releaseAssets("", map[string][]byte{"cfo.exe": []byte("new build"), "goblins-window.exe": []byte("new window")})
	assets["cfo.exe"] = []byte("a build someone swapped in")
	stub := newStubRelease(t, "v0.5.0", assets)

	_, dir, err := fetchFrom(t, stub, noSignature(t))

	if err == nil || !strings.Contains(err.Error(), "cfo.exe does not match the release's SHA256SUMS") {
		t.Fatalf("Fetch = %v, want cfo.exe refused for its checksum", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the download folder %s is still there (%v); a refused download keeps nothing", dir, statErr)
	}
}

func TestFetchRefusesAReleaseWhoseSumsMissAProgram(t *testing.T) {
	cases := map[string]func(map[string][]byte){
		"no SHA256SUMS": func(a map[string][]byte) { delete(a, "SHA256SUMS") },
		"sums without cfo.exe": func(a map[string][]byte) {
			a["SHA256SUMS"] = []byte(sum(a["goblins-window.exe"]) + "  goblins-window.exe\n")
		},
		"no cfo.exe at all":       func(a map[string][]byte) { delete(a, "cfo.exe") },
		"no install script":       func(a map[string][]byte) { delete(a, "install.ps1") },
		"a sum that is not a sum": func(a map[string][]byte) { a["SHA256SUMS"] = []byte("abc  cfo.exe\n") },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			assets := releaseAssets("", map[string][]byte{"cfo.exe": []byte("new build"), "goblins-window.exe": []byte("new window")})
			change(assets)
			stub := newStubRelease(t, "v0.5.0", assets)

			_, _, err := fetchFrom(t, stub, noSignature(t))

			if err == nil {
				t.Fatalf("Fetch accepted a release with %s", name)
			}
		})
	}
}

func TestFetchRefusesADownloadFromAnotherSite(t *testing.T) {
	stub := newStubRelease(t, "v0.5.0", releaseAssets("", map[string][]byte{"cfo.exe": []byte("new build")}))
	source := stub.source()
	source.Downloads = "https://github.com/fpresta0607/code-goblins/releases/download/"
	state := t.TempDir()
	release, err := Latest(context.Background(), http.DefaultClient, stub.source(), state)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Fetch(context.Background(), http.DefaultClient, source, release, filepath.Join(state, "release"), noSignature(t), nil)

	if err == nil || !strings.Contains(err.Error(), "not from") {
		t.Fatalf("Fetch = %v, want a download from another site refused", err)
	}
}

func TestFetchChecksTheSignatureOfASignedRelease(t *testing.T) {
	cases := []struct {
		name                      string
		status, signer, publisher string
		wantErr                   bool
	}{
		{"signed by its publisher", "Valid", "SIQstack LLC", "SIQstack LLC", false},
		{"publisher differs only in case", "Valid", "SIQstack LLC", "SIQSTACK LLC", false},
		{"unsigned", "NotSigned", "", "SIQstack LLC", true},
		{"signed by someone else", "Valid", "Someone Else", "SIQstack LLC", true},
		{"a signature that does not hold", "HashMismatch", "SIQstack LLC", "SIQstack LLC", true},
		{"an invalid signature with a different publisher case", "HashMismatch", "SIQstack LLC", "SIQSTACK LLC", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub := newStubRelease(t, "v0.5.0", releaseAssets(c.publisher, map[string][]byte{"cfo.exe": []byte("new build"), "goblins-window.exe": []byte("new window")}))
			var read []string
			signedBy := func(_ context.Context, path string) (string, string, error) {
				read = append(read, filepath.Base(path))
				return c.status, c.signer, nil
			}

			download, _, err := fetchFrom(t, stub, signedBy)

			if c.wantErr != (err != nil) {
				t.Fatalf("Fetch = %+v, %v; want an error: %v", download, err, c.wantErr)
			}
			if !c.wantErr && (download.Publisher != c.publisher || len(read) != 2) {
				t.Fatalf("download = %+v after reading the signatures of %v; want both programs checked against %s", download, read, c.publisher)
			}
		})
	}
}

func TestWhatsNewTakesTheChangesInPlainWords(t *testing.T) {
	notes := "**This release is unsigned.**\n\n| File | SHA-256 |\n| --- | --- |\n\n## What's Changed\n" +
		"* feat(board): an update arrives in the Command Center by @fpresta0607 in https://github.com/fpresta0607/code-goblins/pull/396\n" +
		"* fix(install): keep a home in use by @fpresta0607 in https://github.com/fpresta0607/code-goblins/pull/391\n" +
		"* Plain words with **bold** and `code` by @someone in https://github.com/fpresta0607/code-goblins/pull/1\n" +
		"* docs: the fourth by @fpresta0607 in https://github.com/fpresta0607/code-goblins/pull/2\n" +
		"\n## New Contributors\n* @someone made their first contribution\n"

	got := WhatsNew(notes, 3)

	want := []string{"An update arrives in the Command Center", "Keep a home in use", "Plain words with bold and code"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("WhatsNew = %q, want %q", got, want)
	}
	if lines := WhatsNew("**This release is unsigned.**\n", 3); len(lines) != 0 {
		t.Fatalf("WhatsNew of notes with no changes = %q, want none", lines)
	}
}

// The release workflow opens every release's notes with whether it is signed
// and by whom, then a table of each file's SHA-256 (release.yml).
func TestNotesSayWhetherTheReleaseIsSignedAndWhatItsProgramHashesTo(t *testing.T) {
	sum := strings.Repeat("3f", 32)
	cases := []struct {
		name, notes, signing, publisher, cfo string
	}{
		{"unsigned", "**This release is unsigned.** Code Goblins has no code-signing identity yet.\n\n| File | SHA-256 |\n| --- | --- |\n| `cfo.exe` | `" + sum + "` |\n", "unsigned", "", sum},
		{"signed", "The programs of this release are code-signed by **SIQstack LLC**, and the install refuses a download that is not.\n\n| `cfo.exe` | `" + sum + "` |\n", "signed", "SIQstack LLC", sum},
		{"notes that say neither", "## What's Changed\n* a change\n", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			signing, publisher := Signing(c.notes)
			if signing != c.signing || publisher != c.publisher {
				t.Fatalf("Signing = %q, %q; want %q, %q", signing, publisher, c.signing, c.publisher)
			}
			if got := NotedSum(c.notes, Program); got != c.cfo {
				t.Fatalf("NotedSum = %q, want %q", got, c.cfo)
			}
		})
	}
}
