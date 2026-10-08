package boardweb

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Assets serves the board Vite built into dist/board where this checkout
// built it, and the placeholder page where it did not. CI's frontend job
// builds the board and checks that it did before it runs this test, so there
// it proves the build lands where cfo.exe embeds it; the go jobs, which
// build no board, prove the placeholder.
func TestAssetsServeTheBoardTheCheckoutBuilt(t *testing.T) {
	// Arrange
	want, err := os.ReadFile(filepath.Join("dist", "board", "index.html"))
	if errors.Is(err, fs.ErrNotExist) {
		want, err = os.ReadFile(filepath.Join("dist", "index.html"))
	}
	if err != nil {
		t.Fatal(err)
	}

	// Act
	board, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadFile(board, "index.html")

	// Assert
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("Assets serves index.html %q (%v), want this checkout's:\n%s", got, err, want)
	}
}

// The placeholder page says the board was not built and names the command
// that builds it, and keeps the <head> the supervisor writes the page's
// nonce and build into.
func TestThePlaceholderSaysHowToBuildTheBoard(t *testing.T) {
	// Arrange
	page, err := fs.ReadFile(assets, "dist/index.html")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	var missing []string
	for _, want := range []string{"<head>", "board was not built", "<code>npm ci</code>", "<code>npm run build</code>", "<code>frontend</code>"} {
		if !strings.Contains(string(page), want) {
			missing = append(missing, want)
		}
	}

	// Assert
	if len(missing) != 0 {
		t.Errorf("the placeholder page lacks %q:\n%s", missing, page)
	}
}

// Only the placeholder page is committed under dist. Every board pull
// request once committed its own build, whose file names Vite hashes by
// content, so each one merged made every other open one conflict.
func TestOnlyThePlaceholderIsCommitted(t *testing.T) {
	// Act
	out, err := exec.Command("git", "ls-files", "-z", "--", "dist").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	tracked := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")

	// Assert
	if !slices.Equal(tracked, []string{"dist/index.html"}) {
		t.Errorf("git tracks %q under internal/boardweb/dist, want only the placeholder dist/index.html: run git rm -r --cached on the rest", tracked)
	}
}

// Git ignores what npm run build writes under dist, so a build is never
// committed by accident, and never ignores the placeholder.
func TestGitIgnoresTheBoardsBuild(t *testing.T) {
	for path, wantIgnored := range map[string]bool{
		"dist/board/index.html":               true,
		"dist/board/assets/index-JtPN-GDN.js": true,
		"dist/index.html":                     false,
	} {
		t.Run(path, func(t *testing.T) {
			// Act: check-ignore exits 0 for an ignored path and 1 for one
			// that is not.
			err := exec.Command("git", "check-ignore", "-q", "--no-index", "--", path).Run()
			var exit *exec.ExitError
			if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
				t.Fatalf("git check-ignore: %v", err)
			}

			// Assert
			if ignored := err == nil; ignored != wantIgnored {
				t.Errorf("git ignores %s: %t, want %t", path, ignored, wantIgnored)
			}
		})
	}
}
