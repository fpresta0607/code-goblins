package gatehome

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// runtimeFiles are what a running daemon keeps in its home for itself alone
// and the next daemon makes again: they are not carried.
var runtimeFiles = map[string]bool{"socket": true, "daemon.pid": true, "daemon.lock": true}

// moveMarker is the file a copy puts in the Dev Drive folder first and a move
// removes last, so a move ended outright, its terminal closed among other
// ways, leaves a copy the next move knows for its own and replaces.
const moveMarker = ".cfo-gate-home-move"

// makeLink makes a link at path that leads to target, of the kind given.
var makeLink = createLink

// copyPlan is what a move copies from the home into the Dev Drive folder.
type copyPlan struct {
	old, target string
	// kept are the places in target the home's links lead to, which a move
	// by hand put there and the move leaves where they are.
	kept []string
	// leftovers are what else is in target, relative to it: what a move by
	// hand copied there and the home holds newer. They are set aside in
	// aside while the move runs.
	leftovers []string
	aside     string
	isAside   bool
	// made are the places the copy made in target, the links it made again
	// among them, which a failed move removes.
	made []string
}

// planCopy refuses, before the daemon is touched, a home that is itself a
// link, a link of the home into target that leads anywhere but its own place
// there, and a target that holds something no link of the home explains.
// Every other link of the home is carried as a link.
func planCopy(old, target, devDrive string, now time.Time) (*copyPlan, error) {
	if linked, isLink := linkTarget(old); isLink {
		return nil, fmt.Errorf("gate home: %s is a junction to %s, and the move carries only a junction to %s; nothing moved", old, linked, target)
	}
	plan := &copyPlan{old: old, target: target, aside: filepath.Join(devDrive, DevDriveFolder+".left-"+now.UTC().Format("20060102T150405Z"))}
	err := walkLinks(old, func(path, linked string) error {
		if !isUnder(linked, target) {
			return nil
		}
		relative, err := filepath.Rel(old, path)
		if err != nil {
			return err
		}
		if want := filepath.Join(target, relative); !sameFolder(linked, want) {
			return fmt.Errorf("gate home: %s is a link to %s, not to %s, so the move cannot carry it; nothing moved", path, linked, want)
		}
		plan.kept = append(plan.kept, filepath.Join(target, relative))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		return plan, nil
	}
	err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == target {
			return err
		}
		switch {
		case slices.ContainsFunc(plan.kept, func(kept string) bool { return sameFolder(kept, path) }):
		case slices.ContainsFunc(plan.kept, func(kept string) bool { return isUnder(kept, path) }):
			return nil
		default:
			relative, err := filepath.Rel(target, path)
			if err != nil {
				return err
			}
			plan.leftovers = append(plan.leftovers, relative)
		}
		if entry.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	_, markerErr := os.Lstat(filepath.Join(target, moveMarker))
	if len(plan.leftovers) > 0 && len(plan.kept) == 0 && markerErr != nil {
		return nil, fmt.Errorf("gate home: %s already holds %s, and no link of %s leads there, so the move does not copy over it; nothing moved", target, plan.leftovers[0], old)
	}
	return plan, nil
}

// setAside moves the leftovers out of target into aside, on the same drive.
func (p *copyPlan) setAside() error {
	for _, relative := range p.leftovers {
		into := filepath.Join(p.aside, relative)
		if err := os.MkdirAll(filepath.Dir(into), 0o700); err != nil {
			return err
		}
		p.isAside = true
		if err := os.Rename(filepath.Join(p.target, relative), into); err != nil {
			return err
		}
	}
	return nil
}

// restore puts the leftovers back where they were.
func (p *copyPlan) restore() error {
	if !p.isAside {
		return nil
	}
	var errs []error
	for _, relative := range p.leftovers {
		from := filepath.Join(p.aside, relative)
		if _, err := os.Lstat(from); errors.Is(err, os.ErrNotExist) {
			continue
		}
		errs = append(errs, os.Rename(from, filepath.Join(p.target, relative)))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return removeTree(p.aside)
}

// done removes the move's marker and the leftovers once the move is done.
func (p *copyPlan) done() error {
	err := os.Remove(filepath.Join(p.target, moveMarker))
	if !p.isAside {
		return err
	}
	return errors.Join(err, removeTree(p.aside))
}

// copy copies the home into target, less the daemon's runtime files and the
// links a move by hand made into target, each link made again as its own
// kind, then proves every copied file holds the same bytes and every link
// leads where it should. It stops between files once ctx ends.
func (p *copyPlan) copy(ctx context.Context) error {
	if _, err := os.Lstat(p.target); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(p.target, 0o700); err != nil {
			return err
		}
		p.made = append(p.made, p.target)
	}
	marker := filepath.Join(p.target, moveMarker)
	if err := os.WriteFile(marker, []byte("a gate home move copies here; the next move replaces what it left\n"), 0o600); err != nil {
		return err
	}
	p.made = append(p.made, marker)
	isMade := func(path string) bool {
		return slices.ContainsFunc(p.made, func(made string) bool { return isUnder(path, made) })
	}
	err := p.walk(func(source, destination string, entry fs.DirEntry, linked string, isLink bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := os.Lstat(destination); err == nil {
			if entry.IsDir() && !isLink {
				return nil
			}
			return fmt.Errorf("%s is already there", destination)
		}
		if isLink || !isMade(destination) {
			p.made = append(p.made, destination)
		}
		switch {
		case isLink:
			return p.copyLink(source, destination, linked)
		case entry.IsDir():
			return os.Mkdir(destination, 0o700)
		}
		return copyFile(source, destination)
	})
	if err != nil {
		return err
	}
	return p.walk(func(source, destination string, entry fs.DirEntry, linked string, isLink bool) error {
		switch {
		case isLink:
			return p.sameLink(source, destination, linked)
		case entry.IsDir():
			return nil
		}
		return sameFile(source, destination)
	})
}

// walk calls visit for each folder, file and link of the home the copy
// carries, with where it goes in target and, for a link, where it leads. It
// never walks into a link, and leaves out the links a move by hand made into
// target, which stay where they lead.
func (p *copyPlan) walk(visit func(source, destination string, entry fs.DirEntry, linked string, isLink bool) error) error {
	return filepath.WalkDir(p.old, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == p.old {
			return err
		}
		relative, err := filepath.Rel(p.old, path)
		if err != nil {
			return err
		}
		if runtimeFiles[relative] {
			return nil
		}
		linked, isLink := linkTarget(path)
		if !isLink || !isUnder(linked, p.target) {
			err = visit(path, filepath.Join(p.target, relative), entry, linked, isLink)
		}
		if err == nil && isLink && entry.IsDir() {
			return fs.SkipDir
		}
		return err
	})
}

// copyLink makes the link at source again at destination as its own kind.
func (p *copyPlan) copyLink(source, destination, linked string) error {
	isJunction, isDirectory, err := linkKind(source)
	if err != nil {
		return err
	}
	if err := makeLink(destination, p.carried(linked), isJunction, isDirectory); err != nil {
		return fmt.Errorf("the link %s could not be made again at %s: %w", source, destination, err)
	}
	return nil
}

// carried is where a link that leads to linked leads once copied: a link to
// a place in the home leads to the same place in target, and any other keeps
// where it leads, a relative one as it is written.
func (p *copyPlan) carried(linked string) string {
	if filepath.IsAbs(linked) && isUnder(linked, p.old) {
		if relative, err := filepath.Rel(p.old, linked); err == nil {
			return filepath.Join(p.target, relative)
		}
	}
	return linked
}

// sameLink proves destination is a link of source's kind that leads where
// the copy carries source's.
func (p *copyPlan) sameLink(source, destination, linked string) error {
	want := p.carried(linked)
	got, isLink := linkTarget(destination)
	if !isLink || !sameFolder(got, want) {
		return fmt.Errorf("%s does not lead to %s, where the copy carries the link %s", destination, want, source)
	}
	isSourceJunction, _, err := linkKind(source)
	if err != nil {
		return err
	}
	isJunction, _, err := linkKind(destination)
	if err != nil {
		return err
	}
	if isJunction != isSourceJunction {
		return fmt.Errorf("%s is not the same kind of link as %s", destination, source)
	}
	return nil
}

// discard removes what the copy made in target.
func (p *copyPlan) discard() error {
	var errs []error
	for _, made := range slices.Backward(p.made) {
		errs = append(errs, removeTree(made))
	}
	return errors.Join(errs...)
}

// removeTree removes path and all below it without following a link: each
// link, proved a link, goes as a link alone first, then the rest.
func removeTree(path string) error {
	if _, isLink := linkTarget(path); isLink {
		return removeLink(path)
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := walkLinks(path, func(link, _ string) error { return removeLink(link) }); err != nil {
		return err
	}
	return fsx.RemoveAll(path)
}

// removeLink removes the link at path alone, once Lstat proves it a link.
func removeLink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !isLinkMode(info) {
		return fmt.Errorf("%s is no link, so it is not removed as one", path)
	}
	return os.Remove(path)
}

// isLinkMode is a symbolic link, a junction or another reparse point.
func isLinkMode(info fs.FileInfo) bool {
	return info.Mode().Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0
}

func copyFile(source, destination string) error {
	in, err := fsx.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := fsx.CreateNew(destination, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Sync(), out.Close())
}

// sameFile proves destination holds source's bytes.
func sameFile(source, destination string) error {
	want, err := hashOf(source)
	if err != nil {
		return err
	}
	got, err := hashOf(destination)
	if err != nil {
		return err
	}
	if !bytes.Equal(want, got) {
		return fmt.Errorf("%s does not hold the bytes of %s", destination, source)
	}
	return nil
}

func hashOf(path string) ([]byte, error) {
	file, err := fsx.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}

// walkLinks calls visit for each link below root, a junction among them,
// with the folder it leads to, and does not walk into it.
func walkLinks(root string, visit func(path, linked string) error) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == root || entry.Type().IsRegular() {
			return err
		}
		linked, isLink := linkTarget(path)
		if !isLink {
			return nil
		}
		if err := visit(path, linked); err != nil {
			return err
		}
		if entry.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
}

func sameFolder(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
