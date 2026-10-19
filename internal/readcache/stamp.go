package readcache

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"
)

// racyWindow is how close to now a file's modification time makes its
// state unsettled: a second write within the same tick of the file system's
// clock, of the same size, would leave the same time and size behind, so a
// read of a file changed this recently is never kept. Git judges its own
// index the same way.
const racyWindow = 2 * time.Second

// stamper sums the state of the files a read depends on: the content of the
// small ones, the size and modification time of the rest.
type stamper struct {
	hash      hash.Hash
	now       time.Time
	isSettled bool
}

func newStamper(now time.Time) *stamper {
	return &stamper{hash: sha256.New(), now: now, isSettled: true}
}

func (s *stamper) sum() ([32]byte, bool) {
	var sum [32]byte
	s.hash.Sum(sum[:0])
	return sum, s.isSettled
}

func (s *stamper) unsettle() { s.isSettled = false }

// text adds parts, each with its length, so no two lists of parts sum alike.
func (s *stamper) text(parts ...string) {
	for _, part := range parts {
		_ = binary.Write(s.hash, binary.LittleEndian, uint64(len(part)))
		_, _ = io.WriteString(s.hash, part)
	}
}

// times adds a file's size and modification time, and unsettles the sum
// when the file changed within racyWindow of now.
func (s *stamper) times(size int64, modified time.Time) {
	_ = binary.Write(s.hash, binary.LittleEndian, [2]int64{size, modified.UnixNano()})
	if gap := s.now.Sub(modified); gap < racyWindow && gap > -racyWindow {
		s.unsettle()
	}
}

// file adds a file's size and modification time, or that there is none.
func (s *stamper) file(name string) {
	info, err := os.Stat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		s.text(name, "-")
	case err != nil:
		s.unsettle()
	default:
		s.text(name)
		s.times(info.Size(), info.ModTime())
	}
}

// content adds a small file's content, or that there is none, and returns
// it.
func (s *stamper) content(name string) string {
	data, err := os.ReadFile(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		s.text(name, "-")
		return ""
	case err != nil:
		s.unsettle()
		return ""
	}
	s.text(name, "+", string(data))
	return string(data)
}

// prefix adds the first n bytes of a file, or that there is none.
func (s *stamper) prefix(name string, n int) {
	file, err := os.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		s.text(name, "-")
		return
	}
	if err != nil {
		s.unsettle()
		return
	}
	defer file.Close()
	data := make([]byte, n)
	read, err := io.ReadFull(file, data)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		s.unsettle()
		return
	}
	s.text(name, "+", string(data[:read]))
}

// tree adds every entry under root, by its path from root, a folder as its
// name and a file as its size and modification time, passing over each
// entry skip names and what lies under it. A tree of more than limit
// entries is unsettled rather than read to its end.
func (s *stamper) tree(root string, skip func(relative string) bool, limit int) {
	entries := 0
	var walk func(folder, relative string) bool
	walk = func(folder, relative string) bool {
		listed, err := os.ReadDir(folder)
		if errors.Is(err, fs.ErrNotExist) {
			s.text(relative, "-")
			return true
		}
		if err != nil {
			s.unsettle()
			return false
		}
		for _, entry := range listed {
			name := path.Join(relative, entry.Name())
			if skip != nil && skip(name) {
				continue
			}
			if entries++; entries > limit {
				s.unsettle()
				return false
			}
			if entry.IsDir() {
				s.text(name, "/")
				if !walk(filepath.Join(folder, entry.Name()), name) {
					return false
				}
				continue
			}
			info, err := entry.Info()
			if err != nil {
				s.unsettle()
				return false
			}
			s.text(name)
			s.times(info.Size(), info.ModTime())
		}
		return true
	}
	walk(root, "")
}
