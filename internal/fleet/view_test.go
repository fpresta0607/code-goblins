package fleet

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/monitor"
)

func TestRenderersProjectOneTypedSnapshot(t *testing.T) {
	seen := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	present := true
	snapshot := Snapshot{
		Schema: "fleet-snapshot.v1",
		Home:   `C:\fleet`,
		Tasks: []TaskRow{
			{
				ID:      "active",
				Current: crewstate.Current{State: crewstate.Working, Source: crewstate.SourceEndpoint},
				Monitor: MonitorSummary{Health: monitor.HealthActive, LastSeen: &seen},
				Kind:    "ship",
				Project: `C:\project`,
				Backend: "herdr",
				Endpoint: EndpointSummary{
					Target: "fleet:pane-active",
					Exists: &present,
				},
				Artifact: `C:\fleet\data\active\report.md`,
				Path:     `C:\work\active`,
				Actions:  Actions{Peek: "cfo peek fm-active"},
			},
			{
				ID:      "stale",
				Current: crewstate.Current{State: crewstate.Done, Source: crewstate.SourceStatus},
				Monitor: MonitorSummary{Health: monitor.HealthStale, StaleSeconds: 12, LastSeen: &seen, Escalation: 2, DemandDeepInspection: true},
				Kind:    "scout",
				Backend: "herdr",
				Actions: Actions{Peek: "cfo peek fm-stale"},
			},
			{
				ID:      "unknown",
				Current: crewstate.Current{State: crewstate.Unknown, Source: crewstate.SourceNone},
				Monitor: MonitorSummary{Health: monitor.HealthUnknown},
				Actions: Actions{Peek: "cfo peek fm-unknown"},
			},
		},
		Backlog: BacklogRows{
			Queued: []BacklogRow{
				{ID: "q1", Structured: true, Title: "Queued work", BlockedBy: "prep", BlockedReason: "wait"},
				{Structured: false, Raw: "- manual queued note"},
			},
			Done: []BacklogRow{{ID: "d1", Structured: true, Title: "Done work"}},
		},
		Secondmates: []SecondmateRow{},
	}

	var jsonOutput bytes.Buffer
	if err := RenderJSON(&jsonOutput, snapshot); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(jsonOutput.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal rendered JSON: %v", err)
	}
	staleMonitor := decoded.Tasks[1].Monitor
	if decoded.Schema != snapshot.Schema || len(decoded.Tasks) != 3 || staleMonitor.Health != monitor.HealthStale || staleMonitor.StaleSeconds != 12 || staleMonitor.LastSeen == nil || !staleMonitor.LastSeen.Equal(seen) || staleMonitor.Escalation != 2 || !staleMonitor.DemandDeepInspection || decoded.Secondmates == nil {
		t.Errorf("JSON projection = %+v, want full typed snapshot", decoded)
	}

	var markdown bytes.Buffer
	if err := RenderMarkdown(&markdown, snapshot); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	output := markdown.String()
	for _, want := range []string{
		"# Fleet View",
		"## Under Way",
		"## Queued",
		"## Done",
		"## Secondmates",
		"| ID | Current | Health | Stale | Last Seen | Escalation | Deep Inspection | Kind | Project | Backend | Endpoint | Artifact | Path | Peek |",
		"| active | working / endpoint | active | - | 2026-08-13T12:00:00Z | 0 | no | ship | C:\\project | herdr | fleet:pane-active (present) | C:\\fleet\\data\\active\\report.md | C:\\work\\active | cfo peek fm-active |",
		"| stale | done / status | stale | 12s | 2026-08-13T12:00:00Z | 2 | yes | scout | - | herdr | unknown | - | - | cfo peek fm-stale |",
		"| unknown | unknown / none | unknown | - | - | 0 | no | - | - | - | unknown | - | - | cfo peek fm-unknown |",
		"| q1 | Queued work | - | - | prep - wait | - |",
		"- manual queued note",
		"Secondmates are not supported in Plan 3.",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("Markdown missing %q:\n%s", want, output)
		}
	}
}

func TestRenderMarkdownUsesSnapshotMonitorValuesWithoutFurtherReads(t *testing.T) {
	seen := time.Date(2026, 8, 13, 14, 30, 0, 0, time.UTC)
	snapshot := Snapshot{
		Schema: "fleet-snapshot.v1",
		Tasks: []TaskRow{
			{ID: "active", Monitor: MonitorSummary{Health: monitor.HealthActive, LastSeen: &seen}},
			{ID: "stale", Monitor: MonitorSummary{Health: monitor.HealthStale, StaleSeconds: 90, LastSeen: &seen, Escalation: 3, DemandDeepInspection: true}},
			{ID: "unknown", Monitor: MonitorSummary{Health: monitor.HealthUnknown}},
		},
		Secondmates: []SecondmateRow{},
	}
	var jsonOutput bytes.Buffer
	if err := RenderJSON(&jsonOutput, snapshot); err != nil {
		t.Fatal(err)
	}
	var markdown bytes.Buffer
	if err := RenderMarkdown(&markdown, snapshot); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"health":"active"`,
		`"health":"stale"`,
		`"stale_seconds":90`,
		`"escalation":3`,
		`"demand_deep_inspection":true`,
		"| active | - / - | active | - | 2026-08-13T14:30:00Z | 0 | no |",
		"| stale | - / - | stale | 1m30s | 2026-08-13T14:30:00Z | 3 | yes |",
		"| unknown | - / - | unknown | - | - | 0 | no |",
	} {
		if !strings.Contains(jsonOutput.String(), want) && !strings.Contains(markdown.String(), want) {
			t.Errorf("rendered projections omit %q:\nJSON:\n%s\nMarkdown:\n%s", want, jsonOutput.String(), markdown.String())
		}
	}
}

func TestRenderMarkdownSeparatesRawBacklogRowsFromLaterTables(t *testing.T) {
	snapshot := Snapshot{
		Backlog: BacklogRows{Queued: []BacklogRow{
			{ID: "q1", Structured: true, Title: "First"},
			{Raw: "- keep this as prose"},
			{ID: "q2", Structured: true, Title: "Second"},
		}},
		Secondmates: []SecondmateRow{},
	}
	var markdown bytes.Buffer
	if err := RenderMarkdown(&markdown, snapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown.String(), "- keep this as prose\n\n| ID | Title |") {
		t.Errorf("Markdown does not separate raw backlog content from a later table:\n%s", markdown.String())
	}
}
