package codegoblins

import (
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// markdownLink matches the target of an inline markdown link.
var markdownLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)
var markdownHeading = regexp.MustCompile(`(?m)^#{1,6} (.+)$`)
var headingPunctuation = regexp.MustCompile(`[^\pL\pN_ -]`)

type contractReference struct {
	file   string
	anchor string
}

func localReferences(name string, data []byte) []contractReference {
	var references []contractReference
	for _, link := range markdownLink.FindAllStringSubmatch(string(data), -1) {
		target, anchor, _ := strings.Cut(link[1], "#")
		if strings.Contains(target, "://") {
			continue
		}
		file := path.Join(path.Dir(name), target)
		if target == "" {
			file = name
		}
		references = append(references, contractReference{file, anchor})
	}
	return references
}

func headingAnchor(heading string) string {
	return strings.ReplaceAll(headingPunctuation.ReplaceAllString(strings.ToLower(strings.TrimSpace(heading)), ""), " ", "-")
}

func headingAnchors(data []byte) []string {
	var anchors []string
	for _, heading := range markdownHeading.FindAllStringSubmatch(string(data), -1) {
		anchors = append(anchors, headingAnchor(heading[1]))
	}
	return anchors
}

// The CFO follows its contract's links, so every relative link in the
// contract's entry points must reach a file the binary carries into a home
// set up without a checkout.
func TestTheContractCarriesEveryFileItLinksTo(t *testing.T) {
	var names []string
	if err := fs.WalkDir(Contract, ".", func(name string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && path.Ext(name) == ".md" {
			names = append(names, name)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		data, err := fs.ReadFile(Contract, name)
		if err != nil {
			t.Fatal(err)
		}
		links := localReferences(name, data)
		if len(links) == 0 && (name == "AGENTS.md" || name == "CLAUDE.md") {
			t.Fatalf("%s has no links, so this test checks nothing", name)
		}
		for _, link := range links {
			body, err := fs.ReadFile(Contract, link.file)
			if err != nil {
				t.Errorf("%s links to %s, which the binary does not carry", name, link.file)
				continue
			}
			if link.anchor != "" && !slices.Contains(headingAnchors(body), link.anchor) {
				t.Errorf("%s links to missing section %s in %s", name, link.anchor, link.file)
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
		for _, link := range localReferences(name, body) {
			if link.file != "AGENTS.md" && !strings.HasPrefix(link.file, "docs/contract/") {
				continue
			}
			reference, err := fs.ReadFile(Contract, link.file)
			if err != nil {
				t.Errorf("%s links to an unshipped contract owner %s: %v", name, link.file, err)
				continue
			}
			if link.anchor != "" && !slices.Contains(headingAnchors(reference), link.anchor) {
				t.Errorf("%s still points to removed contract section %s#%s", name, link.file, link.anchor)
			}
		}
	}
}
