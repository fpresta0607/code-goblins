package release

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Program and Window are the programs an update installs: the one program
// the home runs as cfo.exe and goblins.exe, and the desktop window, which a
// release ships from v0.4.0 on.
const (
	Program = "cfo.exe"
	Window  = "goblins-window.exe"
)

// SignatureReader reads the Authenticode signature of the file at path as
// Windows reports it: its status, such as Valid or NotSigned, and the simple
// name of who signed it.
type SignatureReader func(ctx context.Context, path string) (status, signer string, err error)

// Download is a release's programs, each kept in one folder only once it
// matched the release's SHA256SUMS and, where the release names a publisher,
// carried that publisher's valid signature.
type Download struct {
	// Program is cfo.exe, and Window the desktop window, or empty when the
	// release ships none.
	Program string
	Window  string
	// Publisher is who the release says signed it, empty for an unsigned
	// release.
	Publisher string
	// Sums are the SHA-256 of each program kept, by name.
	Sums map[string]string
}

// publisherLine is where the install script a release publishes names its
// publisher, as tools/pin-installer.ps1 writes it: empty for an unsigned
// release.
var publisherLine = regexp.MustCompile(`(?m)^\s*\$releasePublisher = "([^"\r\n]*)"`)

// Fetch downloads release r's programs from source into dir, a folder of
// their own that it empties first. It reads the release's SHA256SUMS, and
// the publisher its install.ps1 names, before it keeps any program: each
// must match its sum, and, where a publisher is named, carry a valid
// signature by that publisher, read with signedBy. Anything that stops it
// removes dir, so a refused release leaves nothing to run.
func Fetch(ctx context.Context, client *http.Client, source Source, r Release, dir string, signedBy SignatureReader, checking func()) (download Download, err error) {
	if err := os.RemoveAll(dir); err != nil {
		return Download{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Download{}, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	sums, err := fetchText(ctx, client, source, r, "SHA256SUMS", maxSums)
	if err != nil {
		return Download{}, err
	}
	download.Sums, err = parseSums(sums)
	if err != nil {
		return Download{}, err
	}
	script, err := fetchText(ctx, client, source, r, "install.ps1", maxScript)
	if err != nil {
		return Download{}, err
	}
	named := publisherLine.FindSubmatch(script)
	if named == nil {
		return Download{}, fmt.Errorf("release %s's install.ps1 does not say whether it is signed, so nothing was downloaded", r.Tag)
	}
	download.Publisher = string(named[1])
	if _, listed := download.Sums[Program]; !listed {
		return Download{}, fmt.Errorf("release %s's SHA256SUMS lists no %s, so nothing was downloaded", r.Tag, Program)
	}
	checksums := map[string]string{}
	for _, name := range []string{Program, Window} {
		_, listed := download.Sums[name]
		if !listed {
			continue
		}
		checksum, err := fetchProgram(ctx, client, source, r, name, dir)
		if err != nil {
			return Download{}, err
		}
		checksums[name] = checksum
	}
	if checking != nil {
		checking()
	}
	for _, name := range []string{Program, Window} {
		want, listed := download.Sums[name]
		if !listed {
			continue
		}
		if got := checksums[name]; got != want {
			return Download{}, fmt.Errorf("the downloaded %s does not match the release's SHA256SUMS (it is %s, the release lists %s), so nothing was installed", name, got, want)
		}
		partial := filepath.Join(dir, name+".download")
		if download.Publisher != "" {
			if err := checkSigned(ctx, signedBy, partial, name, download.Publisher); err != nil {
				return Download{}, err
			}
		}
		path := filepath.Join(dir, name)
		if err := os.Rename(partial, path); err != nil {
			return Download{}, err
		}
		if name == Program {
			download.Program = path
		} else {
			download.Window = path
		}
	}
	for name := range download.Sums {
		if name != Program && name != Window {
			delete(download.Sums, name)
		}
	}
	return download, nil
}

// assetURL is the address release r publishes name at, refused unless it is
// on the source's download site.
func assetURL(source Source, r Release, name string) (string, error) {
	for _, asset := range r.Assets {
		if asset.Name != name {
			continue
		}
		if !strings.HasPrefix(asset.URL, source.Downloads) {
			return "", fmt.Errorf("release %s's %s is at %s, not from %s, so nothing was downloaded", r.Tag, name, asset.URL, source.Downloads)
		}
		return asset.URL, nil
	}
	return "", fmt.Errorf("release %s publishes no %s, so nothing was downloaded", r.Tag, name)
}

func get(ctx context.Context, client *http.Client, address string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "code-goblins-update")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("%s answered HTTP %d", address, response.StatusCode)
	}
	return response, nil
}

func fetchText(ctx context.Context, client *http.Client, source Source, r Release, name string, limit int64) ([]byte, error) {
	address, err := assetURL(source, r, name)
	if err != nil {
		return nil, err
	}
	response, err := get(ctx, client, address)
	if err != nil {
		return nil, fmt.Errorf("release %s's %s could not be downloaded: %w", r.Tag, name, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("release %s's %s could not be downloaded: %w", r.Tag, name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("release %s's %s is over %d KiB", r.Tag, name, limit>>10)
	}
	return data, nil
}

func fetchProgram(ctx context.Context, client *http.Client, source Source, r Release, name, dir string) (string, error) {
	address, err := assetURL(source, r, name)
	if err != nil {
		return "", err
	}
	response, err := get(ctx, client, address)
	if err != nil {
		return "", fmt.Errorf("release %s's %s could not be downloaded: %w", r.Tag, name, err)
	}
	defer response.Body.Close()
	partial := filepath.Join(dir, name+".download")
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxProgram+1))
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return "", fmt.Errorf("release %s's %s could not be downloaded: %w", r.Tag, name, err)
	}
	if written > maxProgram {
		return "", fmt.Errorf("release %s's %s is over %d MiB", r.Tag, name, maxProgram>>20)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// sumLine is one line of SHA256SUMS as sha256sum writes it: the hex digest,
// a space, and the name, marked binary with an asterisk or not.
var sumLine = regexp.MustCompile(`^([0-9a-fA-F]{64}) [ *](\S+)$`)

func parseSums(data []byte) (map[string]string, error) {
	sums := map[string]string{}
	lines := bufio.NewScanner(bytes.NewReader(data))
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if line == "" {
			continue
		}
		match := sumLine.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("the release's SHA256SUMS has a line that is not a SHA-256 and a name: %q", line)
		}
		sums[match[2]] = strings.ToLower(match[1])
	}
	return sums, lines.Err()
}

func checkSigned(ctx context.Context, signedBy SignatureReader, path, name, publisher string) error {
	status, signer, err := signedBy(ctx, path)
	if err != nil {
		return fmt.Errorf("the signature of the downloaded %s could not be read (%v), so nothing was installed", name, err)
	}
	if status != "Valid" || !strings.EqualFold(signer, publisher) {
		return fmt.Errorf("the downloaded %s is not validly signed by %s (Windows reads %s, by %q), so nothing was installed", name, publisher, status, signer)
	}
	return nil
}
