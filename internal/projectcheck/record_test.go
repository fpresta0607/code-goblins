package projectcheck

import (
	"path/filepath"
	"testing"
)

// A project with no record is the state all 14 projects of the home were in
// when this check was written: `cfo project check` answered with the
// operating system's "cannot find the file specified" and nothing said what
// that costs or what to do.
func TestAMissingProjectRecordIsReportedWithWhereItBelongs(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "record-missing")
	if finding.Severity != High || finding.Area != AreaRecord {
		t.Errorf("record-missing is %s in %s, want high in record", finding.Severity, finding.Area)
	}
	contains(t, "evidence", finding.Evidence, filepath.Join(f.data, "projects", "northwind", "project.json"))
	contains(t, "fix", finding.Fix, "project.json")
	if report.Passed(AreaRecord) {
		t.Error("the record area passed with no record")
	}
}

func TestAValidProjectRecordPasses(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.manifest("project.json", `{"project":"northwind","verification":{"fast":[["go","vet","./..."]]}}`)

	// Act
	report := f.check("go")

	// Assert
	none(t, report, "record-missing")
	finding := only(t, report, "record-valid")
	if finding.Severity != OK {
		t.Errorf("record-valid is %s, want ok", finding.Severity)
	}
	if !report.Passed(AreaRecord) {
		t.Errorf("the record area did not pass:\n%s", report.Text())
	}
}

// `cfo verify` runs the commands of a tier and passes when none fails, so a
// record with no verification command makes it pass with nothing run.
func TestARecordWithNoVerificationCommandIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.manifest("project.json", `{"project":"northwind"}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "tiers-empty")
	if finding.Severity != Medium {
		t.Errorf("tiers-empty is %s, want medium", finding.Severity)
	}
	if report.Passed(AreaRecord) {
		t.Error("the record area passed with no verification command")
	}
}

// A record the loader refuses steers nothing, and the loader's own words say
// which field is wrong.
func TestAnInvalidProjectRecordIsReportedWithTheLoadersReason(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.manifest("project.json", `{"project":"northwind","verfication":{}}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "record-invalid")
	if finding.Severity != High {
		t.Errorf("record-invalid is %s, want high", finding.Severity)
	}
	contains(t, "evidence", finding.Evidence, "verfication")
}

// The record is found by the checkout's folder name, so one that names
// another project describes the wrong one.
func TestARecordNamingAnotherProjectIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.manifest("project.json", `{"project":"southwind"}`)

	// Act
	report := f.check()

	// Assert
	finding := only(t, report, "record-names-another-project")
	contains(t, "evidence", finding.Evidence, "southwind", "northwind")
}

// A verification tier that names a program this machine does not have fails
// every `cfo verify` at that tier.
func TestAVerificationTierNamingAMissingProgramIsReported(t *testing.T) {
	// Arrange
	f := newFixture(t, nil)
	f.manifest("project.json", `{"project":"northwind","verification":{"fast":[["go","vet","./..."]],"full":[["nosuchtool","--all"]]}}`)

	// Act
	report := f.check("go")

	// Assert
	finding := only(t, report, "tier-command-missing")
	if finding.Severity != High {
		t.Errorf("tier-command-missing is %s, want high", finding.Severity)
	}
	contains(t, "evidence", finding.Evidence, "nosuchtool", "verification.full")
}
