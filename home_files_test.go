package codegoblins

import (
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
)

// markdownLink matches the target of an inline markdown link.
var markdownLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)
var markdownHeading = regexp.MustCompile(`(?m)^#{1,6} (.+)$`)
var headingPunctuation = regexp.MustCompile(`[^\pL\pN_ -]`)

// The CFO follows its contract's links, so every relative link in the
// contract's entry points must reach a file the binary carries into a home
// set up without a checkout.
func TestTheContractCarriesEveryFileItLinksTo(t *testing.T) {
	names := []string{"AGENTS.md", "CLAUDE.md"}
	for _, directory := range []string{"docs/contract", "docs/commands"} {
		if err := fs.WalkDir(Contract, directory, func(name string, entry fs.DirEntry, err error) error {
			if err == nil && !entry.IsDir() {
				names = append(names, name)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range names {
		data, err := fs.ReadFile(Contract, name)
		if err != nil {
			t.Fatal(err)
		}
		links := markdownLink.FindAllStringSubmatch(string(data), -1)
		if len(links) == 0 && (name == "AGENTS.md" || name == "CLAUDE.md") {
			t.Fatalf("%s has no links, so this test checks nothing", name)
		}
		for _, link := range links {
			target, anchor, _ := strings.Cut(link[1], "#")
			if strings.Contains(target, "://") {
				continue
			}
			targetPath := path.Join(path.Dir(name), target)
			if target == "" {
				targetPath = name
			}
			body, err := fs.ReadFile(Contract, targetPath)
			if err != nil {
				t.Errorf("%s links to %s, which the binary does not carry", name, target)
				continue
			}
			if anchor != "" {
				found := false
				for _, heading := range markdownHeading.FindAllStringSubmatch(string(body), -1) {
					slug := strings.ReplaceAll(headingPunctuation.ReplaceAllString(strings.ToLower(strings.TrimSpace(heading[1])), ""), " ", "-")
					found = found || slug == anchor
				}
				if !found {
					t.Errorf("%s links to missing section %s in %s", name, anchor, targetPath)
				}
			}
		}
	}
}

func TestExistingGuidesPointToCurrentContractOwners(t *testing.T) {
	for _, name := range []string{"README.md", "docs/install.md", "docs/load-map.md", "docs/native-board.md"} {
		// Arrange
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}

		// Assert
		for _, link := range markdownLink.FindAllStringSubmatch(string(body), -1) {
			target, anchor, _ := strings.Cut(link[1], "#")
			target = path.Clean(path.Join(path.Dir(name), target))
			if target != "AGENTS.md" && !strings.HasPrefix(target, "docs/contract/") {
				continue
			}
			reference, err := fs.ReadFile(Contract, target)
			if err != nil {
				t.Errorf("%s links to an unshipped contract owner %s: %v", name, target, err)
				continue
			}
			if anchor == "" {
				continue
			}
			found := false
			for _, heading := range markdownHeading.FindAllStringSubmatch(string(reference), -1) {
				slug := strings.ReplaceAll(headingPunctuation.ReplaceAllString(strings.ToLower(strings.TrimSpace(heading[1])), ""), " ", "-")
				found = found || slug == anchor
			}
			if !found {
				t.Errorf("%s still points to removed contract section %s#%s", name, target, anchor)
			}
		}
	}
}
