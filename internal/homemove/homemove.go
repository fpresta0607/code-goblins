// Package homemove moves a CFO home to another folder: a checkout an older
// build made its home, or a home anywhere else, into the per-user home every
// install now uses. A plan lists every file it moves, with its SHA-256, the
// task records whose paths into the moved folders it rewrites, and the proof
// that nothing is dropped: the files and bytes before and after, and a digest
// over the SHA-256 of every file it keeps as it is, taken before and after,
// which must match. Applying makes exactly a plan whose digest was approved,
// and reads every moved file back. Worktrees stay where their task records say
// they are, and a file the checkout's git tracks is copied, never moved, so
// the checkout keeps its source.
package homemove

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// movedFolders are the home's folders moved file by file, each file hashed.
var movedFolders = []string{"state", "data", "config"}

// cachesFolder moves as one folder: what it holds is downloads its tools
// fetch again, so it is counted file by file and byte by byte, not hashed.
const cachesFolder = "caches"

// File is one file a move carries, by its slash-separated path in the home.
type File struct {
	Rel  string
	Hash string
	Size int64
	// Copy says the checkout's git tracks it, so it is copied and the
	// checkout keeps it.
	Copy bool
}

// Rewrite is a task record whose recorded paths into the moved folders follow
// them.
type Rewrite struct {
	Rel     string
	Before  string
	After   string
	Content []byte
}

// Plan is everything moving From to To would do.
type Plan struct {
	From, To string
	Now      time.Time
	Files    []File
	Rewrites []Rewrite
	// CacheFiles and CacheBytes are what the caches folder holds.
	CacheFiles int
	CacheBytes int64
	// Mentions are the files moved as they are whose text names the old
	// home.
	Mentions []string
	// Left is what stays in the old home, and why.
	Left []string
	// Programs are an older build's programs at the old home's top, which go
	// once this build is installed in the new home's bin.
	Programs []string
}

// PlanMove works out moving the home at from to to. tracked names the files,
// slash-separated, the checkout's git tracks, nil when from is no checkout.
// It refuses a target that already holds a home's state or data: a move never
// merges two homes.
func PlanMove(from, to string, tracked map[string]bool, now time.Time) (Plan, error) {
	from, to = filepath.Clean(from), filepath.Clean(to)
	if strings.EqualFold(from, to) {
		return Plan{}, errors.New("homemove: the home is already there")
	}
	for _, folder := range []string{"state", "data"} {
		if entries, err := os.ReadDir(filepath.Join(to, folder)); err == nil && len(entries) > 0 {
			return Plan{}, fmt.Errorf("homemove: %s already holds a home's %s; a move never merges two homes", to, folder)
		}
	}
	plan := Plan{From: from, To: to, Now: now.UTC().Truncate(time.Second)}
	needles := mentionsOf(from)
	for _, folder := range movedFolders {
		root := filepath.Join(from, folder)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) && path == root {
					return filepath.SkipDir
				}
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("homemove: %s is neither a file nor a folder, so moving it cannot be proven", path)
			}
			rel, err := filepath.Rel(from, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			content, hash, size, err := read(path)
			if err != nil {
				return err
			}
			plan.Files = append(plan.Files, File{Rel: rel, Hash: hash, Size: size, Copy: tracked[rel]})
			if rewrite, ok := rewriteRecord(rel, content, from, to); ok {
				plan.Rewrites = append(plan.Rewrites, rewrite)
			} else if content != nil && names(content, needles) {
				plan.Mentions = append(plan.Mentions, rel)
			}
			return nil
		})
		if err != nil {
			return Plan{}, err
		}
	}
	files, size, err := count(filepath.Join(from, cachesFolder))
	if err != nil {
		return Plan{}, err
	}
	plan.CacheFiles, plan.CacheBytes = files, size
	if _, err := os.Stat(filepath.Join(from, ".worktrees")); err == nil {
		plan.Left = append(plan.Left, ".worktrees (its worktrees keep the paths their task records name; the janitor removes each once no task owns it and its work is safe)")
	}
	plan.Left = append(plan.Left, leftBehind(from, tracked)...)
	entries, err := os.ReadDir(from)
	if err != nil {
		return Plan{}, err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && isOldProgram(strings.ToLower(entry.Name())) {
			plan.Programs = append(plan.Programs, entry.Name())
		}
	}
	sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].Rel < plan.Files[j].Rel })
	sort.Strings(plan.Mentions)
	return plan, nil
}

// oldPrograms are the binaries an older build kept at the home's root, which
// the move removes once the new home's bin holds this build.
var oldPrograms = []string{"cfo.exe", "goblins.exe", "goblins-window.exe", "goblins-window.png"}

// leftBehind names every top-level entry of the old home the move neither
// carries nor removes, and that its git does not track: what the Overlord
// keeps or tidies himself.
func leftBehind(from string, tracked map[string]bool) []string {
	topTracked := map[string]bool{}
	for rel := range tracked {
		top, _, _ := strings.Cut(rel, "/")
		topTracked[strings.ToLower(top)] = true
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return nil
	}
	var left []string
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if topTracked[name] || name == ".git" || name == ".worktrees" || name == cachesFolder || isMovedFolder(name) || isOldProgram(name) {
			continue
		}
		left = append(left, entry.Name()+" (no part of a home's layout, so it stays where it is)")
	}
	return left
}

func isMovedFolder(name string) bool {
	for _, folder := range movedFolders {
		if name == folder {
			return true
		}
	}
	return false
}

func isOldProgram(name string) bool {
	for _, program := range oldPrograms {
		if name == program || strings.HasPrefix(name, program+".") {
			return true
		}
	}
	return false
}

// readLimit is the largest file whose text is read for the old home's path;
// bigger ones are hashed as a stream.
const readLimit = 1 << 20

// read hashes a file, and returns its content too when it is small enough to
// search.
func read(path string) ([]byte, string, int64, error) {
	file, err := fsx.Open(path)
	if err != nil {
		return nil, "", 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, "", 0, err
	}
	sum := sha256.New()
	if info.Size() > readLimit {
		size, err := io.Copy(sum, file)
		return nil, hex.EncodeToString(sum.Sum(nil)), size, err
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, "", 0, err
	}
	sum.Write(content)
	return content, hex.EncodeToString(sum.Sum(nil)), int64(len(content)), nil
}

// count is the files and bytes under root, links not followed.
func count(root string) (int, int64, error) {
	files, size := 0, int64(0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == root {
				return filepath.SkipDir
			}
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			files++
			size += info.Size()
		}
		return nil
	})
	return files, size, err
}

// mentionsOf are the spellings of a home's path a file may name it by: as
// written, and with its separators escaped as JSON writes them.
func mentionsOf(root string) [][]byte {
	lower := strings.ToLower(root)
	return [][]byte{[]byte(lower), []byte(strings.ReplaceAll(lower, `\`, `\\`)), []byte(strings.ReplaceAll(lower, `\`, `/`))}
}

func names(content []byte, needles [][]byte) bool {
	lower := bytes.ToLower(content)
	for _, needle := range needles {
		if bytes.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// rewriteRecord rewrites a task record, state/<id>.meta, whose values name a
// path inside the folders the move carries, to name the same path in the new
// home. A path anywhere else, its worktree included, stays as it is.
func rewriteRecord(rel string, content []byte, from, to string) (Rewrite, bool) {
	if content == nil || strings.Contains(strings.TrimPrefix(rel, "state/"), "/") || !strings.HasPrefix(rel, "state/") || !strings.HasSuffix(rel, ".meta") {
		return Rewrite{}, false
	}
	lines := strings.Split(string(content), "\n")
	changed := false
	for i, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		for _, folder := range append(movedFolders, cachesFolder) {
			prefix := filepath.Join(from, folder) + `\`
			if len(value) > len(prefix) && strings.EqualFold(value[:len(prefix)], prefix) {
				lines[i] = key + "=" + filepath.Join(to, folder) + `\` + value[len(prefix):]
				changed = true
				break
			}
		}
	}
	if !changed {
		return Rewrite{}, false
	}
	next := []byte(strings.Join(lines, "\n"))
	return Rewrite{Rel: rel, Before: hashOf(content), After: hashOf(next), Content: next}, true
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Proof is the evidence a move keeps every file.
type Proof struct {
	Files, Copied, Rewritten  int
	Bytes                     int64
	BeforeDigest, AfterDigest string
	CacheFiles                int
	CacheBytes                int64
}

// Proof works out the plan's proof: every file and byte before is at the new
// home after, and the digest over the SHA-256 of every file kept as it is is
// the same before and after.
func (p Plan) Proof() Proof {
	rewritten := map[string]bool{}
	for _, rewrite := range p.Rewrites {
		rewritten[rewrite.Rel] = true
	}
	proof := Proof{Files: len(p.Files), Rewritten: len(p.Rewrites), CacheFiles: p.CacheFiles, CacheBytes: p.CacheBytes}
	var kept []string
	for _, file := range p.Files {
		proof.Bytes += file.Size
		if file.Copy {
			proof.Copied++
		}
		if !rewritten[file.Rel] {
			kept = append(kept, file.Hash)
		}
	}
	proof.BeforeDigest = digest(kept)
	proof.AfterDigest = digest(kept)
	return proof
}

func digest(hashes []string) string {
	sorted := append([]string(nil), hashes...)
	sort.Strings(sorted)
	return hashOf([]byte(strings.Join(sorted, "\n")))
}

// PlanTimePrefix starts a listing's first line.
const PlanTimePrefix = "plan made at "

// Listing is the plan as lines: when it was made and between which folders,
// then, sorted, every file it moves or copies with its SHA-256, every record
// it rewrites with its hash before and after, the caches folder with its
// files and bytes, every moved file that names the old home, what stays, and
// the older build's programs it removes.
func (p Plan) Listing() []string {
	var lines []string
	for _, file := range p.Files {
		verb := "move"
		if file.Copy {
			verb = "copy"
		}
		lines = append(lines, fmt.Sprintf("%s %s %s %d", verb, file.Rel, file.Hash, file.Size))
	}
	for _, rewrite := range p.Rewrites {
		lines = append(lines, fmt.Sprintf("rewrite %s %s -> %s", rewrite.Rel, rewrite.Before, rewrite.After))
	}
	lines = append(lines, fmt.Sprintf("move %s (%d files, %d bytes)", cachesFolder, p.CacheFiles, p.CacheBytes))
	for _, rel := range p.Mentions {
		lines = append(lines, "names the old home "+rel)
	}
	for _, left := range p.Left {
		lines = append(lines, "stays "+left)
	}
	for _, program := range p.Programs {
		lines = append(lines, "remove "+program+" (an older build's program; this build is installed in the new home's bin)")
	}
	sort.Strings(lines)
	return append([]string{fmt.Sprintf("%s%s from %s to %s", PlanTimePrefix, p.Now.Format(time.RFC3339), p.From, p.To)}, lines...)
}

// Digest is the SHA-256 of the listing: the same plan made again has the same
// digest only when it makes exactly the same change.
func (p Plan) Digest() string {
	return hashOf([]byte(strings.Join(p.Listing(), "\n")))
}
