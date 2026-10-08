package voice

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// verifiedName is the record a part's folder carries once its archive
// matched its checksum: that checksum and the size of each file kept.
const verifiedName = "verified.json"

type verified struct {
	SHA256 string           `json:"sha256"`
	Files  map[string]int64 `json:"files"`
}

// ready says why a part cannot be used, or nil: its folder holds the files
// the archive with this checksum gave.
func (v *Voice) ready(part Part) error {
	missing := fmt.Errorf("%s %s is not fetched", part.Name, part.Version)
	data, err := fsx.ReadFile(filepath.Join(v.folder(part), verifiedName))
	if err != nil {
		return missing
	}
	var record verified
	if err := json.Unmarshal(data, &record); err != nil || record.SHA256 != part.SHA256 {
		return missing
	}
	for _, file := range part.Files {
		name := path.Base(file)
		info, err := os.Stat(filepath.Join(v.folder(part), name))
		size, known := record.Files[name]
		if err != nil || !known || info.Size() != size {
			return missing
		}
	}
	return nil
}

// fetch downloads a part's archive, or takes the one placed by hand beside
// the parts' folders, and keeps its files only when the archive matches its
// checksum, holds every file named, and no program in it links a networking
// library. The archive itself is never kept. Each fetch downloads to a file
// of its own, so the install's fetch and the board's can run at once.
func (v *Voice) fetch(ctx context.Context, part Part, progress func(done int64)) error {
	address, err := url.Parse(part.URL)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(v.Dir, 0o700); err != nil {
		return err
	}
	archive := filepath.Join(v.Dir, path.Base(address.Path))
	if _, err := os.Stat(archive); errors.Is(err, os.ErrNotExist) {
		placed := archive
		if archive, err = v.download(ctx, part, progress); err != nil {
			return fmt.Errorf("%s %s could not be downloaded: %w. Dictation needs it once: connect to the internet and dictate again, or download %s yourself and save it as %s; it is checked against its SHA-256 before it is used", part.Name, part.Version, err, part.URL, placed)
		}
	}
	defer func() { _ = fsx.Remove(archive) }()
	sum, err := checksum(archive)
	if err != nil {
		return err
	}
	if sum != part.SHA256 {
		return fmt.Errorf("the download of %s %s does not match its checksum (its SHA-256 is %s, the setting names %s), so nothing was kept", part.Name, part.Version, sum, part.SHA256)
	}
	unpacked, err := os.MkdirTemp(v.Dir, "."+part.Name+"-unpack-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(unpacked) }()
	sizes, err := unpack(archive, unpacked, part.Files)
	if err != nil {
		return fmt.Errorf("%s %s: %w", part.Name, part.Version, err)
	}
	if err := offline(unpacked); err != nil {
		return fmt.Errorf("%s %s was refused: %w", part.Name, part.Version, err)
	}
	record, err := json.Marshal(verified{SHA256: part.SHA256, Files: sizes})
	if err != nil {
		return err
	}
	if err := fsx.AtomicWriteFile(filepath.Join(unpacked, verifiedName), record); err != nil {
		return err
	}
	if err := os.RemoveAll(v.folder(part)); err != nil {
		return err
	}
	if err := os.Rename(unpacked, v.folder(part)); err != nil {
		// Another fetch of the same part may have put it in place first.
		if v.ready(part) == nil {
			return nil
		}
		return err
	}
	return nil
}

// supersededPrefix starts the name a replaced part's folder is moved to
// before it is removed.
const supersededPrefix = ".superseded-"

// removeSuperseded removes the folders of the engines and models these
// settings no longer pin. A folder is first moved aside whole, so one still
// in use, such as an engine an earlier build's worker has loaded, cannot be
// moved and stays whole for a later fetch to remove, rather than being half
// removed under the build using it.
func (v *Voice) removeSuperseded() {
	entries, err := os.ReadDir(v.Dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		// Moved aside by a fetch that could not finish removing it.
		if entry.IsDir() && strings.HasPrefix(entry.Name(), supersededPrefix) {
			_ = os.RemoveAll(filepath.Join(v.Dir, entry.Name()))
		}
	}
	for _, name := range v.superseded() {
		aside, err := os.MkdirTemp(v.Dir, supersededPrefix)
		if err != nil {
			return
		}
		if err := os.Rename(filepath.Join(v.Dir, name), filepath.Join(aside, name)); err != nil {
			_ = os.Remove(aside)
			continue
		}
		_ = os.RemoveAll(aside)
	}
}

// superseded names the folders here of engines and models these settings no
// longer pin: each a fetch made, as its record says, that is neither the
// pinned engine's nor the pinned model's.
func (v *Voice) superseded() []string {
	entries, err := os.ReadDir(v.Dir)
	if err != nil {
		return nil
	}
	pinned := map[string]bool{filepath.Base(v.folder(v.Settings.Engine)): true, filepath.Base(v.folder(v.Settings.Model)): true}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || pinned[name] || strings.HasPrefix(name, ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(v.Dir, name, verifiedName)); err == nil {
			names = append(names, name)
		}
	}
	return names
}

// Replaces says an engine or model these settings no longer pin is here, as
// after an update to a build that pins a newer one, so fetching now
// replaces it.
func (v *Voice) Replaces() bool {
	return len(v.superseded()) > 0
}

// redirect follows a download sent on only to another https address, and no
// further than ten times.
func redirect(next *http.Request, via []*http.Request) error {
	if next.URL.Scheme != "https" {
		return fmt.Errorf("the download was sent on to %s, which is not https", next.URL.Redacted())
	}
	if len(via) >= 10 {
		return errors.New("the download was sent on more than 10 times")
	}
	return nil
}

// download writes the part's archive to a file of its own in the temporary
// folder, where one an ended install leaves is cleared with the rest, and
// returns it, telling progress how much of it has arrived.
func (v *Voice) download(ctx context.Context, part Part, progress func(done int64)) (string, error) {
	client := v.Client
	if client == nil {
		client = &http.Client{CheckRedirect: redirect}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, part.URL, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the server answered %s", response.Status)
	}
	out, err := os.CreateTemp("", "code-goblins-"+part.Name+"-")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, &counted{from: response.Body, tell: progress})
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = fsx.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// counted tells how many bytes have been read through it.
type counted struct {
	from io.Reader
	done int64
	tell func(done int64)
}

func (c *counted) Read(buffer []byte) (int, error) {
	n, err := c.from.Read(buffer)
	if n > 0 {
		c.done += int64(n)
		c.tell(c.done)
	}
	return n, err
}

func checksum(file string) (string, error) {
	in, err := fsx.Open(file)
	if err != nil {
		return "", err
	}
	defer in.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, in); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// unpack writes the named files of a .tar.bz2 archive into dir, each under
// its own name alone, and returns their sizes. A file is named by its path
// under the archive's one top folder.
func unpack(archive, dir string, files []string) (map[string]int64, error) {
	in, err := fsx.Open(archive)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	wanted := map[string]bool{}
	for _, file := range files {
		wanted[file] = true
	}
	sizes := map[string]int64{}
	entries := tar.NewReader(bzip2.NewReader(in))
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the archive could not be read: %w", err)
		}
		_, inside, _ := strings.Cut(path.Clean(header.Name), "/")
		if header.Typeflag != tar.TypeReg || !wanted[inside] {
			continue
		}
		name := path.Base(inside)
		out, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
		if err != nil {
			return nil, err
		}
		size, err := io.Copy(out, entries)
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
		sizes[name] = size
		delete(wanted, inside)
	}
	for _, file := range files {
		if wanted[file] {
			return nil, fmt.Errorf("the archive does not hold %s", file)
		}
	}
	return sizes, nil
}
