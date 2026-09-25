package codegoblins

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// markdownLink matches the target of an inline markdown link.
var markdownLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// The CFO follows its contract's links, so every relative link in the
// contract's entry points must reach a file the binary carries into a home
// set up without a checkout.
func TestTheContractCarriesEveryFileItLinksTo(t *testing.T) {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		data, err := fs.ReadFile(Contract, name)
		if err != nil {
			t.Fatal(err)
		}
		links := markdownLink.FindAllStringSubmatch(string(data), -1)
		if len(links) == 0 {
			t.Fatalf("%s has no links, so this test checks nothing", name)
		}
		for _, link := range links {
			target, _, _ := strings.Cut(link[1], "#")
			if target == "" || strings.Contains(target, "://") {
				continue
			}
			if _, err := fs.Stat(Contract, path.Join(path.Dir(name), target)); err != nil {
				t.Errorf("%s links to %s, which the binary does not carry", name, target)
			}
		}
	}
}
