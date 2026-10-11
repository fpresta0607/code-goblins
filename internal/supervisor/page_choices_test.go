package supervisor

import (
	"os"
	"path/filepath"
	"testing"
)

// A goblin's page that asks the Overlord to pick declares its question in a
// data-lavish-choices block. While AFK mode is on the CFO answers it, so its
// choices travel on the CFO's decision as a notify writes them, the
// recommended one marked; a page that declares none, several questions, or
// choices a decision cannot offer, travels with none.
func TestPageOptionsAreTheOneQuestionAPageDeclares(t *testing.T) {
	block := func(json string) string {
		return `<script type="application/json" data-lavish-choices>` + json + `</script>`
	}
	for name, test := range map[string]struct {
		body, want string
	}{
		"one question":                  {block(`{"id": "plan", "question": "Fix it next?", "options": ["Fix it next", "Keep 300 s"], "recommended": "Fix it next"}`), " options: Fix it next (Recommended) | Keep 300 s"},
		"one question in a list":        {block(`[{"id": "plan", "question": "Fix it next?", "options": ["Fix it next", "Keep 300 s"]}]`), " options: Fix it next | Keep 300 s"},
		"attributes in another order":   {`<SCRIPT data-lavish-choices type="application/json">{"options": ["Build it", "Change it"]}</SCRIPT>`, " options: Build it | Change it"},
		"no block":                      {`<p>Three mockups</p>`, ""},
		"two questions":                 {block(`[{"id": "a", "options": ["Yes", "No"]}, {"id": "b", "options": ["Tabs", "Spaces"]}]`), ""},
		"two blocks":                    {block(`{"id": "a", "options": ["Yes", "No"]}`) + block(`{"id": "b", "options": ["Tabs", "Spaces"]}`), ""},
		"a block that does not parse":   {block(`{"options": ["Yes", `), ""},
		"a choice twice":                {block(`{"options": ["Yes", "Yes"]}`), ""},
		"an empty choice":               {block(`{"options": ["Yes", " "]}`), ""},
		"a choice that splits the list": {block(`{"options": ["Yes | No", "Later"]}`), ""},
		"a choice that names the mark":  {block(`{"options": ["See options: below", "Later"]}`), ""},
		"a recommendation not offered":  {block(`{"options": ["Yes", "No"], "recommended": "Maybe"}`), ""},
		"nine choices":                  {block(`{"options": ["1a", "2b", "3c", "4d", "5e", "6f", "7g", "8h", "9i"]}`), ""},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			page := filepath.Join(t.TempDir(), "page.html")
			if err := os.WriteFile(page, []byte("<html><body>"+test.body+"</body></html>"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			got := PageOptions(page)

			// Assert
			if got != test.want {
				t.Errorf("PageOptions = %q, want %q", got, test.want)
			}
		})
	}
}

// A page that cannot be read declares nothing the decision can offer.
func TestPageOptionsOfAPageThatCannotBeReadAreNone(t *testing.T) {
	if got := PageOptions(filepath.Join(t.TempDir(), "gone.html")); got != "" {
		t.Errorf("PageOptions = %q, want none", got)
	}
}
