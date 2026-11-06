package memory

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

// Synthetic task cases follow the public fleet contracts, not private home
// instructions. Expected IDs are the relevance oracle, independent of ranking.
func TestRetrievalEvaluation(t *testing.T) {
	dir := t.TempDir()
	definition := Catalog{Version: 1}
	for _, row := range []struct{ id, project, task, state, location, text string }{
		{"rule", "", "", "", "warm", "Release requires explicit approval.\n"},
		{"ports", "northwind", "serve", "", "cold", "Development port is 4300; localhost listener. <!--a:2026-10-30-->\n"},
		{"auth", "northwind", "login", "", "cold", "Credentials expire; refresh authentication before dispatch. <!--a:2026-10-30-->\n"},
		{"other-port", "other", "serve", "", "cold", "Development port is 8200. <!--a:2026-10-30-->\n"},
		{"hold", "northwind", "release", "held", "warm", "Hold release until approval arrives. <!--a:2026-10-30-->\n"},
		{"finished", "northwind", "old-release", "completed", "cold", "Release checklist from the completed session. <!--a:2026-10-30-->\n"},
		{"old-cache", "northwind", "cache", "", "cold", "Cache port was 4100. <!--a:2026-09-01-->\n"},
		{"new-cache", "northwind", "cache", "", "cold", "Cache port is 4400. <!--a:2026-10-30-->\n"},
	} {
		authority := "fact"
		if row.id == "rule" {
			authority = "ruling"
		}
		entry := Entry{ID: row.id, End: len(row.text), SHA256: Hash([]byte(row.text)), Authority: authority, Location: row.location, Projects: []string{row.project}, Tasks: []string{row.task}, Topics: []string{row.id}, Trigger: row.id, TaskState: row.state}
		if row.project == "" {
			entry.Projects, entry.Tasks = nil, nil
		}
		if row.id == "new-cache" {
			entry.Supersedes = []string{"old-cache"}
		}
		path := "memory/" + row.id + ".md"
		writeMemoryFile(t, dir, path, []byte(row.text))
		definition.Sources = append(definition.Sources, Source{Path: path, SHA256: Hash([]byte(row.text)), Entries: []Entry{entry}})
	}
	writeCatalog(t, dir, definition)
	catalog, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		query Query
		want  []string
	}{
		{Query{Search: "port", Project: "northwind", Limit: 5}, []string{"ports", "old-cache", "new-cache"}},
		{Query{Search: "port", Project: "other", Limit: 5}, []string{"other-port"}},
		{Query{Search: "credentials", Project: "northwind", Limit: 5}, []string{"auth"}},
		{Query{Search: "cache port", Project: "northwind", Limit: 5}, []string{"old-cache", "new-cache"}},
		{Query{Search: "release", Project: "northwind", Limit: 5}, nil},
		{Query{Search: "release", Task: "old-release", Limit: 5}, []string{"finished"}},
		{Query{Search: "unicorn", Limit: 5}, nil},
		{Query{Search: "sign in", Project: "northwind", Limit: 5}, []string{"auth"}},
	}
	hits, relevant, extras, vectorHits, vectorExtras := 0, 0, 0, 0, 0
	started := time.Now()
	for _, fixture := range cases {
		selected, err := catalog.selectEntries(fixture.query)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range catalog.Sources {
			for _, entry := range source.Entries {
				if selected[entry.ID] && entry.Location == "cold" {
					if slices.Contains(fixture.want, entry.ID) {
						hits++
					} else {
						extras++
					}
				}
			}
		}
		relevant += len(fixture.want)
	}
	for range 1000 {
		for _, fixture := range cases {
			if _, err := catalog.selectEntries(fixture.query); err != nil {
				t.Fatal(err)
			}
		}
	}
	lexicalNanos := time.Since(started).Nanoseconds() / (1001 * int64(len(cases)))
	started = time.Now()
	for _, fixture := range cases {
		for _, id := range sparseVectorCandidates(catalog, fixture.query) {
			if slices.Contains(fixture.want, id) {
				vectorHits++
			} else {
				vectorExtras++
			}
		}
	}
	for range 1000 {
		for _, fixture := range cases {
			sparseVectorCandidates(catalog, fixture.query)
		}
	}
	vectorNanos := time.Since(started).Nanoseconds() / (1001 * int64(len(cases)))
	if hits != 8 || relevant != 9 || extras != 0 {
		t.Fatalf("relevance changed: hits=%d relevant=%d extras=%d", hits, relevant, extras)
	}
	first, err := catalog.Retrieve(Query{Search: "port", Project: "northwind", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	var delivered []string
	for _, word := range strings.Fields(first) {
		if strings.HasPrefix(word, "delivery=") {
			delivered = append(delivered, strings.TrimPrefix(word, "delivery="))
		}
	}
	repeated, err := catalog.Retrieve(Query{Search: "port", Project: "northwind", Limit: 5, Delivered: delivered})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "STOW REQUIRED") || !strings.Contains(first, "old-cache") || !strings.Contains(first, "Supersession candidates") || !strings.Contains(first, "Hold release") {
		t.Fatal("stale or conflicting authority hidden")
	}
	// Compare three identical requested range deliveries against the pre-change
	// renderer, including its provenance and diagnostics. New receipt overhead
	// and the discovery warning count against the new delivery, not the baseline.
	selected, err := catalog.selectEntries(cases[0].query)
	if err != nil {
		t.Fatal(err)
	}
	var legacy strings.Builder
	for _, source := range catalog.Sources {
		for _, entry := range source.Entries {
			if !selected[entry.ID] {
				continue
			}
			status := catalog.status[entry.ID]
			fmt.Fprintf(&legacy, "\n[%s] %s bytes %d:%d sha256=%s authority=%s tier=%s\n", entry.ID, source.Path, entry.Start, entry.End, entry.SHA256, entry.Authority, status.Tier)
			if status.IsStale || status.IsLegacy || status.IsGrace || status.PendingCapture != "" {
				fmt.Fprintf(&legacy, "STOW REQUIRED: stale=%t legacy=%t grace=%t pending-capture=%s; retain until the owning pass resolves it.\n", status.IsStale, status.IsLegacy, status.IsGrace, status.PendingCapture)
			}
			if len(entry.Supersedes) > 0 {
				fmt.Fprintf(&legacy, "Supersession candidates (no automatic retirement): %s\n", strings.Join(entry.Supersedes, ", "))
			}
			legacy.Write(catalog.content[source.Path][entry.Start:entry.End])
			legacy.WriteByte('\n')
		}
	}
	before, after := 3*legacy.Len(), len(first)+2*len(repeated)
	if after >= before {
		t.Fatal("acknowledged repeat did not reduce delivered bytes")
	}
	started = time.Now()
	for range 10 {
		loaded, err := Load(dir, referenceDate)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := loaded.Retrieve(cases[0].query); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("EVIDENCE capture=three-identical-range-deliveries bytes_before=%d tokens_before=%d bytes_after=%d tokens_after=%d first_before=%d first_after=%d lexical_hits=%d relevant=%d false_positives=%d lexical_ns=%d sparse_cosine_hits=%d sparse_cosine_false_positives=%d sparse_cosine_ns=%d load_retrieve_mean_ns=%d cases=%d", before, Tokens(before), after, Tokens(after), legacy.Len(), len(first), hits, relevant, extras, lexicalNanos, vectorHits, vectorExtras, vectorNanos, time.Since(started).Nanoseconds()/10, len(cases))
}

// Evaluation-only bag-of-words cosine, fused with exact lexical selection.
// This has no downloaded model and makes no semantic-embedding claim.
func sparseVectorCandidates(catalog Catalog, query Query) []string {
	lexical, _ := catalog.selectEntries(query)
	queryCounts := map[string]float64{}
	for _, word := range searchWords(query.Search) {
		queryCounts[word]++
	}
	var result []string
	for _, source := range catalog.Sources {
		for _, entry := range source.Entries {
			if entry.Location != "cold" || query.Project != "" && !slices.Contains(entry.Projects, query.Project) || query.Task != "" && !slices.Contains(entry.Tasks, query.Task) || entry.TaskState == "completed" && !query.IncludeCompleted && query.Task == "" {
				continue
			}
			counts := map[string]float64{}
			for _, word := range searchWords(string(catalog.content[source.Path])) {
				counts[word]++
			}
			dot, norm, queryNorm := 0.0, 0.0, 0.0
			for word, count := range counts {
				dot += count * queryCounts[word]
				norm += count * count
			}
			for _, count := range queryCounts {
				queryNorm += count * count
			}
			if lexical[entry.ID] || dot/math.Sqrt(norm*queryNorm) > 0 {
				result = append(result, entry.ID)
			}
		}
	}
	return result
}

func TestFleetEncodingEvaluation(t *testing.T) {
	type row struct {
		Task    string `json:"task"`
		Project string `json:"project"`
		State   string `json:"state"`
		Detail  string `json:"detail"`
	}
	rows := []row{{"serve", "northwind", "active", "port 4300"}, {"release", "northwind", "held", "approval, then release"}, {"login", "other", "active", "refresh\ncredentials"}, {"cache", "northwind", "completed", "Unicode: café"}}
	pretty, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	compact, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	fmt.Fprintf(&output, "tasks[%d]{task,project,state,detail}:\n", len(rows))
	for _, row := range rows {
		fmt.Fprintf(&output, "  %q,%q,%q,%q\n", row.Task, row.Project, row.State, row.Detail)
	}
	// The quoted tabular subset uses JSON string escapes, including newlines.
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")[1:]
	for index, line := range lines {
		fields, err := csv.NewReader(strings.NewReader(strings.TrimSpace(line))).Read()
		if err != nil || len(fields) != 4 {
			t.Fatalf("invalid tabular record: %s %v", line, err)
		}
		for column, want := range []string{rows[index].Task, rows[index].Project, rows[index].State, rows[index].Detail} {
			// CSV already removes quotes; JSON escape interpretation restores text.
			var decoded string
			if err := json.Unmarshal([]byte("\""+fields[column]+"\""), &decoded); err != nil || decoded != want {
				t.Fatalf("lost field: %s %v", fields[column], err)
			}
		}
	}
	t.Logf("EVIDENCE fleet_rows=%d pretty_json_bytes=%d pretty_json_tokens=%d compact_json_bytes=%d compact_json_tokens=%d quoted_toon_bytes=%d quoted_toon_tokens=%d", len(rows), len(pretty), Tokens(len(pretty)), len(compact), Tokens(len(compact)), output.Len(), Tokens(output.Len()))
}
