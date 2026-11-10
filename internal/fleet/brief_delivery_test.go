package fleet

import (
	"strings"
	"testing"
)

func TestBriefDeliveryReadsOnlyTheDeliverySection(t *testing.T) {
	for _, test := range []struct {
		name, brief string
		want        map[string]string
	}{
		{"no brief", "", map[string]string{}},
		{"no Delivery section", "## Task\nmode: direct-PR\nharness: codex\n", map[string]string{}},
		{"a line before any heading", "mode: local-only\n## Delivery\nmode: no-mistakes\n", map[string]string{"mode": "no-mistakes"}},
		{"task text that names every choice", "## Task\nShip it.\nmode: direct-PR\nharness: codex\nmodel: quoted-model\neffort: low\n## Delivery\nmode: no-mistakes\nready: true\n", map[string]string{"mode": "no-mistakes", "ready": "true"}},
		{"a section after Delivery", "## Delivery\nmode: no-mistakes\n## Notes\nmode: local-only\neffort: low\n", map[string]string{"mode": "no-mistakes"}},
		{"the first value of a name", "## Delivery\nmodel: \nmodel: first\nmodel: second\n", map[string]string{"model": "first"}},
		{"padded lines and Windows line ends", "## Delivery\r\n  harness : codex  \r\n\tmodel:provider/model:id\r\n", map[string]string{"harness": "codex", "model": "provider/model:id"}},
		{"a name in another case is another name", "## Delivery\nMode: local-only\nmode: no-mistakes\n", map[string]string{"Mode": "local-only", "mode": "no-mistakes"}},
		{"prose in the section", "## Delivery\nKeep ready: false while editing.\nmode: direct-PR\n", map[string]string{"Keep ready": "false while editing.", "mode": "direct-PR"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			named := BriefDelivery([]byte(test.brief))

			// Assert
			if len(named) != len(test.want) {
				t.Fatalf("named %v, want %v", named, test.want)
			}
			for key, value := range test.want {
				if named[key] != value {
					t.Errorf("%s=%q, want %q; named %v", key, named[key], value, named)
				}
			}
		})
	}
}

// Issue 1925: the mode a start dispatches is the one readiness validated, so
// no line readiness passes over may name another.
func TestBriefDeliveryNamesTheModeReadinessValidated(t *testing.T) {
	for _, test := range []struct{ name, brief string }{
		{"authored", authoredBrief},
		{"a mode line in the task text", strings.Replace(authoredBrief, "## Task\n", "## Task\nmode: local-only\n", 1)},
		{"a quoted mode line in the task text", strings.Replace(authoredBrief, "## Task\n", "## Task\n> mode: local-only\n  Mode: local-only\n", 1)},
		{"a mode line in another case under Delivery", strings.Replace(authoredBrief, "## Delivery\n", "## Delivery\nMode: local-only\n", 1)},
		{"Windows line ends", strings.ReplaceAll(authoredBrief, "\n", "\r\n")},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			if (test.brief == authoredBrief) != (test.name == "authored") {
				t.Fatalf("the authored brief changed shape, so this case adds no line to it: %q", authoredBrief)
			}
			if err := ValidateBriefReadiness([]byte(test.brief)); err != nil {
				t.Fatalf("readiness refused the brief, so it validated no mode: %v", err)
			}

			// Act
			mode := BriefDelivery([]byte(test.brief))["mode"]

			// Assert
			if mode != "no-mistakes" {
				t.Fatalf("mode=%q, want no-mistakes, the one Delivery mode readiness validated", mode)
			}
		})
	}
}
