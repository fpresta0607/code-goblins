package siqspeak

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHistoryReadsNewestValidEntriesWithoutChangingTheFile(t *testing.T) {
	for _, test := range []struct {
		name       string
		content    string
		isMissing  bool
		want       []string
		hasSkipped bool
	}{
		{name: "missing", isMissing: true, want: []string{}},
		{name: "empty", want: []string{}},
		{name: "malformed", content: "{broken}\n{\"text\":\"hello\",\"timestamp\":\"12:45:00\",\"time_epoch\":123}\n", want: []string{"hello"}, hasSkipped: true},
		{name: "newest five", content: "{\"text\":\"one\"}\n{\"text\":\"two\"}\n{\"text\":\"three\"}\n{\"text\":\"four\"}\n{\"text\":\"five\"}\n{\"text\":\"six\"}\n", want: []string{"six", "five", "four", "three", "two"}},
		{name: "invalid entry", content: "null\n{\"text\":null}\n{\"text\":\"  \"}\n{\"text\":7}\n{\"text\":\"valid\"}\n", want: []string{"valid"}, hasSkipped: true},
		{name: "partial append", content: "{\"text\":\"complete\"}\n{\"text\":\"unfinished", want: []string{"complete"}, hasSkipped: true},
		{name: "unicode and multiline", content: "{\"text\":\"hello 世界\\nsecond line\"}\n", want: []string{"hello 世界\nsecond line"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "transcriptions.jsonl")
			if !test.isMissing {
				if err := os.WriteFile(filename, []byte(test.content), 0600); err != nil {
					t.Fatal(err)
				}
			}

			history, err := readHistory(filename)

			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(history.Entries))
			for _, entry := range history.Entries {
				got = append(got, entry.Text)
			}
			if !reflect.DeepEqual(got, test.want) || history.HasSkipped != test.hasSkipped {
				t.Fatalf("entries = %v, skipped = %v; want %v, %v", got, history.HasSkipped, test.want, test.hasSkipped)
			}
			if history.Entries == nil {
				t.Fatal("empty history must encode as an array")
			}
			data, err := os.ReadFile(filename)
			if test.isMissing {
				if !os.IsNotExist(err) {
					t.Fatal("reader created the missing file")
				}
			} else if err != nil || string(data) != test.content {
				t.Fatal("reader changed history")
			}
		})
	}
}

func TestHistoryBoundsReadsAndDropsUnpasteableEntries(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "transcriptions.jsonl")
	oversized, err := json.Marshal(map[string]string{"text": strings.Repeat("x", 65536)})
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("old log data\n", 100000) + string(oversized) + "\n{\"text\":\"newest\",\"raw_text\":\"private raw\",\"timestamp\":\"12:45:00\",\"time_epoch\":123}\n"
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	history, err := readHistory(filename)

	if err != nil {
		t.Fatal(err)
	}
	if len(history.Entries) != 1 || history.Entries[0].Text != "newest" || !history.HasSkipped {
		t.Fatal("bounded read lost the recent valid entry")
	}
	encoded, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private raw") {
		t.Fatal("raw transcript leaked")
	}
	if history.Entries[0].Timestamp != "12:45:00" || history.Entries[0].TimeEpoch != 123 {
		t.Fatal("timestamp was lost")
	}
}

func TestHistoryReportsUnreadableFileWithoutItsContents(t *testing.T) {
	_, err := readHistory(t.TempDir())
	if err == nil {
		t.Fatal("a directory must not be treated as empty history")
	}
}
