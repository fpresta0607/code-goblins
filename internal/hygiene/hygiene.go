package hygiene

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Candidate struct {
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Kind     string `json:"kind"`
	Evidence string `json:"evidence"`
}

func Tests(root string) ([]Candidate, error) {
	var out []Candidate
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			n := d.Name()
			if n == ".git" || n == "vendor" || n == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		n := strings.ToLower(d.Name())
		if !(strings.Contains(n, "test") || strings.Contains(n, "spec")) {
			return nil
		}
		f, e := os.Open(p)
		if e != nil {
			return nil
		}
		defer f.Close()
		s := bufio.NewScanner(f)
		line := 0
		for s.Scan() {
			line++
			txt := s.Text()
			lo := strings.ToLower(txt)
			kind := ""
			switch {
			case strings.Contains(lo, "t.skip(") || strings.Contains(lo, "pytest.mark.skip") || strings.Contains(lo, "@skip"):
				kind = "skipped-test"
			case strings.Contains(lo, "deprecated") && strings.Contains(lo, "flag"):
				kind = "deprecated-feature-flag"
			case strings.Contains(lo, "legacy") && strings.Contains(lo, "implementation"):
				kind = "superseded-implementation"
			}
			if kind != "" {
				out = append(out, Candidate{File: p, Line: line, Kind: kind, Evidence: strings.TrimSpace(txt)})
			}
		}
		return nil
	})
	return out, err
}
