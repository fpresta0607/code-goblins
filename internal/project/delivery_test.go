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
	for _, test := range []struct{ posture, surface, risk, requested, want string }{
		{"no-mistakes-prod-only", "internal", "low", "", "direct-PR"},
		{"no-mistakes-prod-only", "product", "low", "", "no-mistakes"},
		{"direct-PR", "internal", "low", "", "direct-PR"},
		{"direct-PR", "product", "low", "", "direct-PR"},
		{"local-only", "internal", "low", "", "local-only"},
		{"local-only", "product", "low", "", "local-only"},
		{"no-mistakes", "internal", "low", "", "no-mistakes"},
		{"", "internal", "low", "", "no-mistakes"},
		{"invalid", "internal", "low", "", "no-mistakes"},
		{"direct-PR", "mixed", "low", "", "no-mistakes"},
		{"direct-PR", "unknown", "low", "", "no-mistakes"},
		{"local-only", "mixed", "low", "", "no-mistakes"},
		{"local-only", "unknown", "low", "", "no-mistakes"},
		{"direct-PR", "internal", "credentials", "", "no-mistakes"},
		{"direct-PR", "internal", "publication", "", "no-mistakes"},
		{"direct-PR", "internal", "authorization", "", "no-mistakes"},
		{"direct-PR", "internal", "state-recovery", "", "no-mistakes"},
		{"direct-PR", "internal", "migration", "", "no-mistakes"},
		{"direct-PR", "internal", "unknown", "", "no-mistakes"},
		{"direct-PR", "internal", "low", "no-mistakes", "no-mistakes"},
		{"local-only", "internal", "low", "local-only", "local-only"},
	} {
		t.Run(fmt.Sprintf("%s/%s/%s/%s", test.posture, test.surface, test.risk, test.requested), func(t *testing.T) {
			// Arrange
			manifest := Manifest{Delivery: DeliveryPolicy{Posture: test.posture}, Verification: Verification{Fast: []Command{{"go", "test", "./..."}}}}
			brief := fmt.Sprintf("## Delivery\nsurface: %s\nrisk: %s\nreason: changes the approved report output\n", test.surface, test.risk)

			// Act
			selection := SelectDelivery(manifest, brief, test.requested, "")

			// Assert
			if selection.Mode != test.want || selection.Reason == "" || len(selection.Verification.Fast) != 1 {
				t.Fatalf("selection=%+v; want mode=%s", selection, test.want)
			}
		})
	}
}

func TestDeliverySelectionKeepsAnUnauthorizedNarrowerRequestStrict(t *testing.T) {
	const assessed = "## Delivery\nsurface: internal\nrisk: low\nreason: change the approved report output\n"
	for _, test := range []struct{ name, posture, brief, requested string }{
		{"unconfigured project", "", "", "local-only"},
		{"unconfigured project", "", "", "direct-PR"},
		{"strict posture", "no-mistakes", assessed, "direct-PR"},
		{"conditional posture with product work", "no-mistakes-prod-only", strings.Replace(assessed, "internal", "product", 1), "direct-PR"},
		{"unassessed queued brief", "direct-PR", "## Delivery\n\nkind: ship\nmode: direct-PR\n", ""},
		{"pre-upgrade root brief", "local-only", "## Delivery\n\nkind: ship\nmode: direct-PR\n", ""},
		{"standing posture for another mode", "direct-PR", assessed, "local-only"},
		{"unknown mode", "direct-PR", assessed, "yolo"},
	} {
		t.Run(test.name+"/"+test.requested, func(t *testing.T) {
			// Arrange
			manifest := Manifest{Delivery: DeliveryPolicy{Posture: test.posture}, Verification: Verification{Fast: []Command{{"git", "status"}}}}

			// Act
			selection := SelectDelivery(manifest, test.brief, test.requested, "")

			// Assert
			if selection.Mode != "no-mistakes" || !strings.HasPrefix(selection.Reason, "requested ") || !strings.Contains(selection.Reason, "not authorized") {
				t.Fatalf("selection=%+v; want strict delivery that records why the request was not taken", selection)
			}
		})
	}
}

func TestDeliverySelectionHonorsOnlyAConcreteCEOExceptionWithChecks(t *testing.T) {
	for _, test := range []struct {
		name, exception, want string
		fast                  []Command
	}{
		{"concrete exception", "Overlord approved local delivery for this fixture", "local-only", []Command{{"git", "status"}}},
		{"blank exception", "  ", "no-mistakes", []Command{{"git", "status"}}},
		{"multi-line exception", "approved\nlater", "no-mistakes", []Command{{"git", "status"}}},
		{"exception without checks", "Overlord approved local delivery for this fixture", "no-mistakes", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			manifest := Manifest{Verification: Verification{Fast: test.fast}}

			// Act
			selection := SelectDelivery(manifest, "", "local-only", test.exception)

			// Assert
			if selection.Mode != test.want || selection.Reason == "" {
				t.Fatalf("selection=%+v; want mode=%s", selection, test.want)
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
				selection := SelectDelivery(Manifest{Delivery: DeliveryPolicy{Posture: posture}, Verification: Verification{Fast: []Command{{"git", "status"}}}}, brief, "", "")
				if selection.Mode != "no-mistakes" {
					t.Fatalf("posture=%s surface=%s uncertain intake selected %+v", posture, surface, selection)
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
					selection := SelectDelivery(manifest, brief, "", "")

					// Assert
					if selection.Mode != "no-mistakes" {
						t.Fatalf("sensitive or uncertain intake selected %+v", selection)
					}
				})
			}
		}
	}
}

func TestDeliverySelectionStaysFrozenWhenProjectPostureChanges(t *testing.T) {
	selection := SelectDelivery(Manifest{}, "", "", "")
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
		selection := SelectDelivery(manifest, brief, "", "")

		// Assert
		want := "no-mistakes"
		if len(commands) == 1 && len(commands[0]) == 2 {
			want = "direct-PR"
		}
		if selection.Mode != want {
			t.Fatalf("commands=%q selection=%+v", commands, selection)
		}
	}
}

func TestDeliverySelectionDoesNotDowngradeAKnownHighRiskAsk(t *testing.T) {
	// Arrange
	manifest := Manifest{Delivery: DeliveryPolicy{Posture: "direct-PR"}, Verification: Verification{Fast: []Command{{"git", "status"}}}}
	brief := "## Task\nApply a schema migration.\n## Delivery\nsurface: internal\nrisk: low\nreason: update operator tooling\n"

	// Act
	selection := SelectDelivery(manifest, brief, "", "")

	// Assert
	if selection.Mode != "no-mistakes" || selection.Risk != "migration" {
		t.Fatalf("selection=%+v", selection)
	}
}
