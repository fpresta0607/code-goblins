package verify

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopReserveReadsItsSettingOrKeepsTheDefault(t *testing.T) {
	for _, test := range []struct {
		name    string
		setting string
		want    Headroom
	}{
		{name: "no setting", want: DefaultDesktopReserve},
		{name: "whole gigabytes", setting: `{"physical_gib": 3, "commit_gib": 1}`, want: Headroom{PhysicalAvailable: 3 << 30, CommitAvailable: 1 << 30}},
		{name: "fractions", setting: `{"physical_gib": 1.5, "commit_gib": 2.5}`, want: Headroom{PhysicalAvailable: 3 << 29, CommitAvailable: 5 << 29}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			if test.setting != "" {
				if err := os.WriteFile(filepath.Join(dir, "reserve.json"), []byte(test.setting), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			reserve, err := DesktopReserve(dir)

			// Assert
			if err != nil || reserve != test.want {
				t.Fatalf("reserve = %+v, %v, want %+v", reserve, err, test.want)
			}
		})
	}
}

func TestDesktopReserveRefusesASettingItCannotTrust(t *testing.T) {
	for name, setting := range map[string]string{
		"empty":                   ``,
		"not json":                `physical 2`,
		"one dimension":           `{"physical_gib": 2}`,
		"zero":                    `{"physical_gib": 0, "commit_gib": 2}`,
		"negative":                `{"physical_gib": 2, "commit_gib": -1}`,
		"at the admission floor":  `{"physical_gib": 4, "commit_gib": 2}`,
		"an unknown setting":      `{"physical_gib": 2, "commit_gib": 2, "estimate_gib": 6}`,
		"trailing data":           `{"physical_gib": 2, "commit_gib": 2} {}`,
		"a number in quotes":      `{"physical_gib": "2", "commit_gib": 2}`,
		"a directory in its name": "",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			path := filepath.Join(dir, "reserve.json")
			var err error
			if name == "a directory in its name" {
				err = os.Mkdir(path, 0o700)
			} else {
				err = os.WriteFile(path, []byte(setting), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}

			// Act
			reserve, err := DesktopReserve(dir)

			// Assert
			if err == nil || reserve != (Headroom{}) {
				t.Fatalf("an untrusted setting gave %+v, %v", reserve, err)
			}
		})
	}
}
