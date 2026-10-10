package projectcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fetchedAt sets when the checkout last fetched, and returns the time a run
// two days later starts.
func fetchedAt(t *testing.T, f fixture) time.Time {
	t.Helper()
	fetched := time.Date(2026, 10, 8, 14, 2, 0, 0, time.Local)
	if err := os.Chtimes(filepath.Join(f.checkout, ".git", "FETCH_HEAD"), fetched, fetched); err != nil {
		t.Fatal(err)
	}
	return fetched.Add(48 * time.Hour)
}

// A line that says what commit was read did not say how old that reading
// is. Five of eleven checkouts had a default branch behind their remote, and
// a spawn fetches first, so a goblin got files the check never read. The
// check now says when the branch was fetched and asks the remote where it
// is, by a read that changes nothing in the repository.
func TestTheReadingSaysWhenItWasFetchedAndThatTheRemoteHasMoved(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{".no-mistakes.yaml": "commands:\n  test: \"pytest -q\"\n", "AGENTS.md": "Test with `pytest -q`.\n"})
	f.origin()
	read := f.output("rev-parse", "--short", "origin/main")
	f.write("NEW.md", "the remote gains a commit\n")
	f.commit("a commit the checkout pushes and then forgets")
	f.git("push", "--quiet", "origin", "HEAD:main")
	moved := f.output("rev-parse", "--short", "HEAD")
	f.git("update-ref", "refs/remotes/origin/main", "HEAD~1")
	f.git("reset", "--quiet", "--hard", "HEAD~1")
	options := f.options("pytest")
	options.Now = func() time.Time { return fetchedAt(t, f) }
	options.Now()
	before := f.output("for-each-ref", "--format=%(refname) %(objectname)")

	// Act
	report := f.run(options)

	// Assert
	finding := only(t, report, "remote-moved")
	if finding.Severity != Medium || finding.Area != AreaCheckout {
		t.Errorf("remote-moved is %s in %s, want medium in checkout", finding.Severity, finding.Area)
	}
	contains(t, "evidence", finding.Evidence, read, moved, "last fetched 2026-10-08 14:02, 2 days before this run")
	saidRead := 0
	for _, line := range report.Lines {
		if text := line.Text(); strings.Contains(text, "ead at origin/main") {
			saidRead++
			if !strings.Contains(text, "last fetched 2026-10-08 14:02") {
				t.Errorf("a line says what was read and not when it was fetched: %s", text)
			}
		}
	}
	if saidRead < 3 {
		t.Errorf("%d lines say what was read at origin/main, want at least the gate's, the instructions' and the test setup's:\n%s", saidRead, report.Text())
	}
	if after := f.output("for-each-ref", "--format=%(refname) %(objectname)"); after != before {
		t.Errorf("the check moved a ref of the repository:\nbefore %s\nafter  %s", before, after)
	}
	if info, err := os.Stat(filepath.Join(f.checkout, ".git", "FETCH_HEAD")); err != nil || !info.ModTime().Equal(time.Date(2026, 10, 8, 14, 2, 0, 0, time.Local)) {
		t.Errorf("the check fetched: FETCH_HEAD is now from %v (%v)", info.ModTime(), err)
	}
	if report.Passed(AreaCheckout) {
		t.Error("the checkout area passed with a reading the remote has moved past")
	}
}

// A remote at the commit that was read is said too, so a reading that is
// current does not look the same as one nobody compared.
func TestAReadingTheRemoteAgreesWithSaysSo(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.origin()
	options := f.options()
	options.Now = func() time.Time { return fetchedAt(t, f) }

	// Act
	report := f.run(options)

	// Assert
	none(t, report, "remote-moved")
	finding := only(t, report, "checkout-read")
	if finding.Severity != OK {
		t.Errorf("checkout-read is %s, want ok", finding.Severity)
	}
	contains(t, "says", finding.Says, "origin/main", "the remote is at the same commit")
	contains(t, "evidence", finding.Evidence, "last fetched 2026-10-08 14:02, 2 days before this run", "git ls-remote origin")
	if !report.Passed(AreaCheckout) {
		t.Errorf("the checkout area did not pass:\n%s", report.Text())
	}
}

// A remote that does not answer leaves the question open, and the line says
// so in place of guessing either way. What git wrote about the failure can
// hold the remote's address, which can hold a credential, so none of it is
// printed.
func TestARemoteThatDoesNotAnswerIsSaidAndItsAddressIsNotPrinted(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.origin()
	gone := filepath.Join(filepath.Dir(f.checkout), "user-token-in-address.git")
	f.git("remote", "set-url", "origin", gone)

	// Act
	report := f.check()

	// Assert
	none(t, report, "remote-moved")
	finding := only(t, report, "remote-unanswered")
	if finding.Severity != Low || finding.Area != AreaCheckout {
		t.Errorf("remote-unanswered is %s in %s, want low in checkout", finding.Severity, finding.Area)
	}
	if strings.Contains(report.Text(), "user-token-in-address") {
		t.Errorf("the report prints the remote's address:\n%s", report.Text())
	}
}

// A checkout with no remote has nothing to compare with, and its lines name
// the folder's own commit with no fetch to speak of.
func TestACheckoutWithNoRemoteSaysTheFoldersOwnCommitWasRead(t *testing.T) {
	// Arrange
	f := newFixture(t, map[string]string{"AGENTS.md": "Test with `pytest -q`.\n"})

	// Act
	report := f.check("pytest")

	// Assert
	finding := only(t, report, "checkout-read")
	contains(t, "says", finding.Says, "names no default branch of a remote")
	if strings.Contains(report.Text(), "last fetched") {
		t.Errorf("a line speaks of a fetch in a checkout with no remote:\n%s", report.Text())
	}
}

// A project the home holds files for and this machine has no checkout of
// answered with an error and exit 1. It is a finding, and what needs no
// repository is still read: the record, and whether the loaders take the
// home's other files. An area that needs the repository is said to be not
// assessed, never passed.
func TestAProjectWithNoCheckoutIsAFindingAndTheHomesFilesAreStillRead(t *testing.T) {
	for _, test := range []struct {
		name  string
		make  func(t *testing.T, folder string)
		named string
	}{
		{"no folder", func(*testing.T, string) {}, "is no folder on this machine"},
		{"a folder that is no git checkout", func(t *testing.T, folder string) { writeFile(t, filepath.Join(folder, "README.md"), "leftover\n") }, "is not a git checkout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, nil)
			gone := filepath.Join(filepath.Dir(f.checkout), "southwind")
			test.make(t, gone)
			for name, content := range map[string]string{
				"project.json":  `{"project":"southwind","verification":{"fast":[["go","vet","./..."]]}}`,
				"auth.json":     `{"project":"southwind","services":[{"name":"github","method":"cli","env":["GITHUB_TOKEN"],"probe":["gh","auth","status"]}]}`,
				"worktree.json": `{"project":"southwind","dependencies":{"strategy":"copy"}}`,
			} {
				writeFile(t, filepath.Join(f.data, "projects", "southwind", name), content)
			}
			options := f.options("go")
			options.Checkout = gone

			// Act
			report := f.run(options)

			// Assert
			finding := only(t, report, "checkout-missing")
			if finding.Severity != High || finding.Area != AreaCheckout {
				t.Errorf("checkout-missing is %s in %s, want high in checkout", finding.Severity, finding.Area)
			}
			contains(t, "evidence", finding.Evidence, gone, test.named)
			if report.Project != "southwind" {
				t.Errorf("the report is for %q, want southwind", report.Project)
			}
			only(t, report, "record-valid")
			contains(t, "evidence", only(t, report, "tier-commands-found").Evidence, "go")
			contains(t, "evidence", only(t, report, "worktree-invalid").Evidence, "copy")
			contains(t, "evidence", only(t, report, "connectors-home-only").Evidence, "github")
			verdict := report.Verdict(Areas)
			contains(t, "verdict", verdict, "checkout failed (1 high)", "record passed", "gate not assessed", "configs failed (1 high, the home's files alone)", "connectors passed (the home's files alone)", "instructions not assessed")
			for _, area := range []string{AreaGate, AreaInstructions} {
				if report.Passed(area) {
					t.Errorf("the %s area passed with no repository read", area)
				}
			}
			if !report.Passed(AreaRecord) {
				t.Errorf("the record area did not pass:\n%s", report.Text())
			}
		})
	}
}

// The name inside auth.json was compared with nothing. One that names
// another project describes the wrong one, as a record that does.
func TestAnAuthManifestNamingAnotherProjectIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.manifest("auth.json", `{"project":"southwind","services":[]}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "auth-names-another-project")
	if finding.Severity != Medium || finding.Area != AreaConnectors {
		t.Errorf("auth-names-another-project is %s in %s, want medium in connectors", finding.Severity, finding.Area)
	}
	contains(t, "evidence", finding.Evidence, "southwind", "northwind")
}
