package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectAcceptsAnExplicitDeliveryPosture(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "project.json")
	if err := os.WriteFile(path, []byte(`{"project":"demo","delivery":{"posture":"no-mistakes-prod-only"},"verification":{"fast":[["go","test","./..."]]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := Load(path)

	// Assert
	if err != nil {
		t.Fatalf("project policy must accept an explicit conditional posture: %v", err)
	}
}

func TestDeliverySelectionUsesPostureAndConcreteRisk(t *testing.T) {
	for _, test := range []struct {
		posture, surface, risk, requested, want string
		isRejected                              bool
	}{
		{"no-mistakes-prod-only", "internal", "low", "", "direct-PR", false},
		{"no-mistakes-prod-only", "product", "low", "", "no-mistakes", false},
		{"direct-PR", "internal", "low", "", "direct-PR", false},
		{"direct-PR", "product", "low", "", "direct-PR", false},
		{"local-only", "internal", "low", "", "local-only", false},
		{"local-only", "product", "low", "", "local-only", false},
		{"no-mistakes", "internal", "low", "", "no-mistakes", false},
		{"", "internal", "low", "", "no-mistakes", false},
		{"invalid", "internal", "low", "", "no-mistakes", false},
		{"direct-PR", "mixed", "low", "", "no-mistakes", false},
		{"direct-PR", "unknown", "low", "", "no-mistakes", false},
		{"local-only", "mixed", "low", "", "no-mistakes", false},
		{"local-only", "unknown", "low", "", "no-mistakes", false},
		{"direct-PR", "internal", "credentials", "", "no-mistakes", false},
		{"direct-PR", "internal", "publication", "", "no-mistakes", false},
		{"direct-PR", "internal", "authorization", "", "no-mistakes", false},
		{"direct-PR", "internal", "state-recovery", "", "no-mistakes", false},
		{"direct-PR", "internal", "migration", "", "no-mistakes", false},
		{"direct-PR", "internal", "unknown", "", "no-mistakes", false},
		{"direct-PR", "internal", "low", "no-mistakes", "no-mistakes", false},
		{"no-mistakes", "product", "low", "direct-PR", "", true},
	} {
		t.Run(fmt.Sprintf("%s/%s/%s/%s", test.posture, test.surface, test.risk, test.requested), func(t *testing.T) {
			// Arrange
			manifest := Manifest{Delivery: DeliveryPolicy{Posture: test.posture}, Verification: Verification{Fast: []Command{{"go", "test", "./..."}}}}
			brief := fmt.Sprintf("## Delivery\nsurface: %s\nrisk: %s\nreason: changes the approved report output\n", test.surface, test.risk)

			// Act
			selection, err := SelectDelivery(manifest, brief, test.requested, "")

			// Assert
			if (err != nil) != test.isRejected || err == nil && (selection.Mode != test.want || selection.Reason == "" || len(selection.Verification.Fast) != 1 || selection.RequiresCI != (test.want != "local-only")) {
				t.Fatalf("selection=%+v error=%v; want mode=%s rejected=%t", selection, err, test.want, test.isRejected)
			}
		})
	}
}

func TestDeliverySelectionRejectsMalformedOrUnprovedSimpleIntake(t *testing.T) {
	for _, posture := range []string{"no-mistakes-prod-only", "direct-PR", "local-only"} {
		for _, surface := range []string{"internal", "product"} {
			for _, brief := range []string{
				"", fmt.Sprintf("## Delivery\nsurface: %s\nrisk: low\n", surface),
				fmt.Sprintf("## Delivery\nsurface: %s\nsurface: product\nrisk: low\nreason: report output\n", surface),
				fmt.Sprintf("## Delivery\nsurface: %s\nrisk: low\nreason: report output\n## Delivery\n", surface),
			} {
				selection, err := SelectDelivery(Manifest{Delivery: DeliveryPolicy{Posture: posture}, Verification: Verification{Fast: []Command{{"git", "status"}}}}, brief, "", "")
				if err != nil || selection.Mode != "no-mistakes" {
					t.Fatalf("posture=%s surface=%s uncertain intake selected %+v, %v", posture, surface, selection, err)
				}
			}
		}
	}
}

func TestDeliverySelectionKeepsSensitiveAndUncertainChangesStrict(t *testing.T) {
	for _, posture := range []string{"no-mistakes", "no-mistakes-prod-only", "direct-PR", "local-only"} {
		for _, surface := range []string{"internal", "product", "mixed", "unknown"} {
			for _, risk := range []string{"credentials", "authorization", "state-recovery", "migration", "publication", "high", "unknown"} {
				t.Run(posture+"/"+surface+"/"+risk, func(t *testing.T) {
					// Arrange
					manifest := Manifest{Delivery: DeliveryPolicy{Posture: posture}, Verification: Verification{Fast: []Command{{"git", "status"}}}}
					brief := fmt.Sprintf("## Delivery\nsurface: %s\nrisk: %s\nreason: change the approved report output\n", surface, risk)

					// Act
					selection, err := SelectDelivery(manifest, brief, "", "")

					// Assert
					if err != nil || selection.Mode != "no-mistakes" || !selection.RequiresCI {
						t.Fatalf("sensitive or uncertain intake selected %+v, %v", selection, err)
					}
				})
			}
		}
	}
}

func TestDeliverySelectionStaysFrozenWhenProjectPostureChanges(t *testing.T) {
	selection, err := SelectDelivery(Manifest{}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	data, hash, err := selection.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDeliverySelection([]byte(data), hash, "direct-PR"); err == nil {
		t.Fatal("mode changed without a new intake")
	}
	if _, err := ReadDeliverySelection([]byte(strings.Replace(data, "no-mistakes", "direct-PR", 1)), hash, "direct-PR"); err == nil {
		t.Fatal("changed receipt was accepted")
	}
	if got, err := ReadDeliverySelection([]byte(data), hash, "no-mistakes"); err != nil || got.Mode != "no-mistakes" {
		t.Fatalf("frozen selection=%+v error=%v", got, err)
	}
}

func TestDeliverySelectionRequiresRealChecksForASimplePath(t *testing.T) {
	for _, commands := range [][]Command{nil, {{}}, {{""}}, {{"git", "status"}}} {
		// Arrange
		manifest := Manifest{Delivery: DeliveryPolicy{Posture: "direct-PR"}, Verification: Verification{Fast: commands}}
		brief := "## Delivery\nsurface: internal\nrisk: low\nreason: approved report change\n"

		// Act
		selection, err := SelectDelivery(manifest, brief, "", "")

		// Assert
		want := "no-mistakes"
		if len(commands) == 1 && len(commands[0]) == 2 {
			want = "direct-PR"
		}
		if err != nil || selection.Mode != want {
			t.Fatalf("commands=%q selection=%+v error=%v", commands, selection, err)
		}
	}
}

func TestDeliverySelectionDoesNotDowngradeAKnownHighRiskAsk(t *testing.T) {
	// Arrange
	manifest := Manifest{Delivery: DeliveryPolicy{Posture: "direct-PR"}, Verification: Verification{Fast: []Command{{"git", "status"}}}}
	brief := "## Task\nApply a schema migration.\n## Delivery\nsurface: internal\nrisk: low\nreason: update operator tooling\n"

	// Act
	selection, err := SelectDelivery(manifest, brief, "", "")

	// Assert
	if err != nil || selection.Mode != "no-mistakes" || selection.Risk != "migration" {
		t.Fatalf("selection=%+v error=%v", selection, err)
	}
}
