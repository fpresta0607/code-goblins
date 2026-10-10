package projectcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// On Windows a folder answers to its name in any letter case. Typing a
// checkout's path in another case made the check report two mediums and
// tell the reader to rename the project in its record and its worktree
// manifest. The project is named as its folder is spelled on disk, and the
// names inside the home's files are compared without regard to case.
func TestAProjectNamedInAnotherLetterCaseIsTheSameProject(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.manifest("project.json", `{"project":"NorthWind","verification":{"fast":[["go","vet","./..."]]}}`)
	f.manifest("worktree.json", `{"project":"Northwind","link":[]}`)
	f.manifest("auth.json", `{"project":"NORTHWIND","services":[]}`)
	options := f.options("go")
	options.Checkout = filepath.Join(filepath.Dir(f.checkout), "NORTHWIND")

	// Act
	report := f.run(options)

	// Assert
	if report.Project != "northwind" {
		t.Errorf("the report is for %q, want northwind, as the folder is spelled on disk", report.Project)
	}
	for _, check := range []string{"record-names-another-project", "worktree-names-another-project", "auth-names-another-project", "checkout-missing"} {
		none(t, report, check)
	}
	if !report.Passed(AreaRecord) || !report.Passed(AreaConfigs) {
		t.Errorf("the record and configs areas did not both pass:\n%s", report.Text())
	}
	if strings.Contains(report.Verdict(Areas), "NORTHWIND") {
		t.Errorf("the verdict spells the project as it was typed: %s", report.Verdict(Areas))
	}
}
