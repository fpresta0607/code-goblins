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
	// made are the places the copy made in target, which a failed move
	// removes.
	made []string
}

// planCopy refuses, before the daemon is touched, a home with a link that
// leads anywhere but the matching place in target, and a target that holds
// something no link of the home explains.
func planCopy(old, target, devDrive string, now time.Time) (*copyPlan, error) {
	if _, isLink := linkTarget(old); isLink {
		return nil, fmt.Errorf("gate home: %s is itself a link; nothing moved", old)
	}
	plan := &copyPlan{old: old, target: target, aside: filepath.Join(devDrive, DevDriveFolder+".left-"+now.UTC().Format("20060102T150405Z"))}
	err := walkLinks(old, func(path, linked string) error {
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
	return fsx.RemoveAll(p.aside)
}

// done removes the move's marker and the leftovers once the move is done.
func (p *copyPlan) done() error {
	err := os.Remove(filepath.Join(p.target, moveMarker))
	if !p.isAside {
		return err
	}
	return errors.Join(err, fsx.RemoveAll(p.aside))
}

// copy copies the home into target, less the daemon's runtime files and what
// its links lead to, then proves every copied file holds the same bytes. It
// stops between files once ctx ends.
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
	err := p.walk(func(source, destination string, entry fs.DirEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := os.Lstat(destination); err == nil {
			if entry.IsDir() {
				return nil
			}
			return fmt.Errorf("%s is already there", destination)
		}
		if !isMade(destination) {
			p.made = append(p.made, destination)
		}
		if entry.IsDir() {
			return os.Mkdir(destination, 0o700)
		}
		return copyFile(source, destination)
	})
	if err != nil {
		return err
	}
	return p.walk(func(source, destination string, entry fs.DirEntry) error {
		if entry.IsDir() {
			return nil
		}
		return sameFile(source, destination)
	})
}

// walk calls visit for each folder and file of the home the copy carries,
// with where it goes in target.
func (p *copyPlan) walk(visit func(source, destination string, entry fs.DirEntry) error) error {
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
		if _, isLink := linkTarget(path); isLink {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		return visit(path, filepath.Join(p.target, relative), entry)
	})
}

// discard removes what the copy made in target.
func (p *copyPlan) discard() error {
	var errs []error
	for _, made := range slices.Backward(p.made) {
		errs = append(errs, fsx.RemoveAll(made))
	}
	return errors.Join(errs...)
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
