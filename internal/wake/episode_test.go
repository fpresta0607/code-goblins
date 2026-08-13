package wake

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishIncrementsGeneration(t *testing.T) {
	dir := t.TempDir()
	gen1, err := PublishEpisode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if gen1 != 1 {
		t.Errorf("gen1 = %d, want 1", gen1)
	}
	gen2, err := PublishEpisode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if gen2 != 2 {
		t.Errorf("gen2 = %d, want 2", gen2)
	}
	data, err := os.ReadFile(filepath.Join(dir, episodeFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "pending:2\n" {
		t.Errorf("file = %q, want %q", data, "pending:2\n")
	}
}

func TestAckMatchingGeneration(t *testing.T) {
	dir := t.TempDir()
	gen, err := PublishEpisode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := AckEpisode(dir, gen); err != nil {
		t.Fatalf("AckEpisode: %v", err)
	}
	ep, err := ReadEpisode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Pending {
		t.Error("Pending = true after ack")
	}
	data, err := os.ReadFile(filepath.Join(dir, episodeFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "acked:1\n" {
		t.Errorf("file = %q, want %q", data, "acked:1\n")
	}
}

func TestAckMismatchReturnsSentinel(t *testing.T) {
	dir := t.TempDir()
	if _, err := PublishEpisode(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := PublishEpisode(dir); err != nil {
		t.Fatal(err)
	}
	if err := AckEpisode(dir, 1); !errors.Is(err, ErrGenerationMismatch) {
		t.Errorf("err = %v, want ErrGenerationMismatch", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, episodeFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "pending:2\n" {
		t.Errorf("file = %q, want %q (unchanged)", data, "pending:2\n")
	}
}

func TestReadEpisodeMissingFileIsZero(t *testing.T) {
	ep, err := ReadEpisode(t.TempDir())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if ep.Pending || ep.Gen != 0 {
		t.Errorf("ep = %+v, want zero Episode", ep)
	}
}

func TestReadEpisodeToleratesMalformedLines(t *testing.T) {
	cases := []string{"", "pending", "pending:notanumber"}
	for _, content := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, episodeFile), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		ep, err := ReadEpisode(dir)
		if err != nil {
			t.Errorf("content %q: err = %v, want nil", content, err)
		}
		if ep.Pending || ep.Gen != 0 {
			t.Errorf("content %q: ep = %+v, want zero Episode", content, ep)
		}
	}
}
