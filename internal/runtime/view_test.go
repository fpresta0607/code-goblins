package runtime

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func render(t *testing.T, report Report) string {
	t.Helper()
	var out bytes.Buffer
	if err := RenderMarkdown(&out, report); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	return out.String()
}

// The acceptance bar for this command is that its output is legible without a
// second command, so every section must be present and each must carry the
// fact the CFO came for.
func TestRenderMarkdownCarriesAllFiveSections(t *testing.T) {
	out := render(t, Build(`C:\dev\code-goblins`, reportFixture()))

	for _, heading := range []string{
		"# Local Runtime",
		"## Containers",
		"### Volumes nothing references",
		"## Local servers",
		"## Machine headroom",
		"### Declared memory limits of running stacks",
		"## Deployed where",
		"## Local testing",
	} {
		if !strings.Contains(out, heading) {
			t.Errorf("output is missing %q", heading)
		}
	}

	for _, fact := range []string{
		// ownership, with its evidence beside it
		"goblin peak-inbox-connect",
		"project peakCraftsman",
		"unowned siqsermon-draft-flow",
		"Evidence:",
		// the restart loop, named with its count
		"RESTART LOOP",
		"1037 times",
		// the stale server verdict and who owns retiring it
		"safe - task peak-compute-to-supabase is finished; cfo reap retires it",
		"cfo reap",
		// headroom, including the footprint that starves the fleet
		"WSL virtual machine: 11.0 GB",
	} {
		if !strings.Contains(out, fact) {
			t.Errorf("output is missing %q", fact)
		}
	}
}

// A degraded source must be named at the top, before any section a reader
// could mistake for empty.
func TestRenderMarkdownPutsDegradedSourcesFirst(t *testing.T) {
	report := Build("", Inventory{Notes: []string{"CONTAINERS UNREADABLE: Docker is not running - start it"}})
	out := render(t, report)
	degraded := strings.Index(out, "## Degraded")
	containers := strings.Index(out, "## Containers")
	if degraded < 0 || containers < 0 || degraded > containers {
		t.Errorf("Degraded at %d, Containers at %d; want the warning first\n%s", degraded, containers, out)
	}
}

func TestRenderMarkdownOnAnEmptyReportSaysSoInEverySection(t *testing.T) {
	out := render(t, Build("", Inventory{}))
	for _, empty := range []string{
		"No containers found.",
		"None: every named volume is mounted by a container.",
		"No listening servers found.",
		"No stack is running.",
		"No project declares a deploy target.",
		"No project declares or implies a local stack.",
	} {
		if !strings.Contains(out, empty) {
			t.Errorf("output is missing %q\n%s", empty, out)
		}
	}
}

// A container label, a command line and a volume name are all somebody else's
// strings. A terminal escape in one must not reach the rendered report, and a
// pipe must not break the table it sits in.
func TestRenderMarkdownNeutralisesHostileValues(t *testing.T) {
	report := Build("", Inventory{
		Containers: []Container{{
			Name:  "evil\x1b[31mred",
			Image: "img|pipe",
			State: "running",
			Stack: "s",
		}},
	})
	out := render(t, report)
	if strings.Contains(out, "\x1b") {
		t.Error("an escape sequence reached the rendered output")
	}
	if !strings.Contains(out, `\u001B`) {
		t.Errorf("the control character was dropped rather than shown\n%s", out)
	}
	if !strings.Contains(out, `img\|pipe`) {
		t.Errorf("a pipe was left unescaped inside a table cell\n%s", out)
	}
}

func TestRenderJSONRoundTripsTheTypedReport(t *testing.T) {
	report := Build(`C:\dev\code-goblins`, reportFixture())
	var out bytes.Buffer
	if err := RenderJSON(&out, report); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var back Report
	if err := json.Unmarshal(out.Bytes(), &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back.Schema != Schema || back.Home != report.Home {
		t.Errorf("schema/home = %q/%q, want %q/%q", back.Schema, back.Home, Schema, report.Home)
	}
	if len(back.Stacks) != len(report.Stacks) || len(back.Servers) != len(report.Servers) {
		t.Errorf("stacks/servers = %d/%d, want %d/%d", len(back.Stacks), len(back.Servers), len(report.Stacks), len(report.Servers))
	}
	// The Markdown projection truncates a long working directory; the JSON one
	// must not, or the full value is lost.
	for index, server := range report.Servers {
		if back.Servers[index].WorkDir != server.WorkDir {
			t.Errorf("server %d work dir = %q, want the whole %q", index, back.Servers[index].WorkDir, server.WorkDir)
		}
	}
}

func TestTruncateKeepsShortValuesWhole(t *testing.T) {
	if got := Truncate("short", 40); got != "short" {
		t.Errorf("Truncate = %q, want it untouched", got)
	}
	if got := Truncate("abcdefghij", 8); got != "abcde..." {
		t.Errorf("Truncate = %q, want %q", got, "abcde...")
	}
}
