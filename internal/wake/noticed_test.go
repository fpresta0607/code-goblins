package wake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// stateEntries is the name of every file and folder directly inside dir.
func stateEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// On 2026-10-09 an acknowledgement of 55 records wrote a notice file for
// each, about 1.5 s apiece on a loaded machine: 84 s. However many records an
// acknowledgement retires, their notices are one write to one file.
func TestAnAcknowledgementKeepsEveryNoticeInOneFile(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	first := map[string]int{}
	last := 0
	for at := range 55 {
		identity := fmt.Sprintf("done/g%d", at)
		record, err := AppendOnce(dir, identity, "notify", "g1", "done: "+identity)
		if err != nil {
			t.Fatal(err)
		}
		first[identity], last = record.Seq, record.Seq
	}
	before := stateEntries(t, dir)

	// Act
	err := AckThrough(dir, last)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var added []string
	for _, name := range stateEntries(t, dir) {
		if !slices.Contains(before, name) {
			added = append(added, name)
		}
	}
	if want := []string{".wake-ack", ".wake-noticed"}; !slices.Equal(added, want) {
		t.Errorf("the acknowledgement of 55 records added %v to the state folder, want %v: the ack floor and one file of notices", added, want)
	}
	if notices, err := os.ReadDir(dir + `\wake-notices`); err == nil {
		t.Errorf("the acknowledgement wrote %d notice files, one for each record", len(notices))
	}
	for identity, seq := range first {
		again, isNew, err := AppendFirst(dir, identity, "notify", "g1", "done: "+identity)
		if err != nil || isNew || again.Seq != seq {
			t.Fatalf("the acknowledged notice %s was appended again: %+v, new = %t, %v", identity, again, isNew, err)
		}
	}
	if pending, err := Pending(dir); err != nil || len(pending) != 0 {
		t.Errorf("the queue holds %v, %v, want nothing", pending, err)
	}
}

// A home acknowledged by a build that kept each notice in a file of its own
// still holds those files, and a retry of one of them is still answered from
// it rather than delivered a second time.
func TestANoticeAnOlderBuildKeptIsStillHonoured(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	kept := Record{Seq: 7, Kind: "notify", Key: "g1", Detail: "done: g1", Once: "done/g1"}
	data, err := json.Marshal(kept)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(kept.Once))
	path := filepath.Join(dir, "wake-notices", hex.EncodeToString(sum[:])+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	again, isNew, err := AppendFirst(dir, kept.Once, "notify", "g1", "done: g1")

	// Assert
	if err != nil || isNew || again.Seq != kept.Seq {
		t.Errorf("the notice an older build kept was appended again: %+v, new = %t, %v", again, isNew, err)
	}
	if pending, err := Pending(dir); err != nil || len(pending) != 0 {
		t.Errorf("the queue holds %v, %v, want nothing", pending, err)
	}
}

// A machine that stops in the middle of a write can leave the notices' last
// line cut off. That costs the notice it was, never the ones kept after it.
func TestACutOffNoticeCostsOnlyItself(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, noticedFile), []byte(`{"seq":1,"kind":"notify","key":"g0","once":"done/g`), 0o600); err != nil {
		t.Fatal(err)
	}
	done, err := AppendOnce(dir, "done/g1", "notify", "g1", "done: g1")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	err = AckThrough(dir, done.Seq)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if again, isNew, err := AppendFirst(dir, "done/g1", "notify", "g1", "done: g1"); err != nil || isNew || again.Seq != done.Seq {
		t.Errorf("the notice kept after a cut off line was appended again: %+v, new = %t, %v", again, isNew, err)
	}
}

// cfo drain --ack-through took the wake lock twice, once for the records and
// once for the recovery episode. One acknowledgement is one hold of the lock:
// the records, the episode and what is left queued all come out of it.
func TestOneAcknowledgementRetiresTheRecordsAndTheEpisodeUnderOneLock(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	done, err := Append(dir, "notify", "g1", "done: g1")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := PublishEpisode(dir)
	if err != nil {
		t.Fatal(err)
	}
	holds := 0
	lockHeld = func() { holds++ }
	t.Cleanup(func() { lockHeld = func() {} })

	// Act
	acknowledged, err := Acknowledge(dir, Ack{Through: &done.Seq, Generation: &generation})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if holds != 1 {
		t.Errorf("the acknowledgement held the wake lock %d times, want once", holds)
	}
	if len(acknowledged.Pending) != 0 || acknowledged.Episode.Pending || acknowledged.Episode.Gen != generation || acknowledged.HasGenerationMoved {
		t.Errorf("the acknowledgement left %+v, want an empty queue and generation %d acknowledged", acknowledged, generation)
	}
	if pending, err := Pending(dir); err != nil || len(pending) != 0 {
		t.Errorf("the queue holds %v, %v, want nothing", pending, err)
	}
	if episode, err := ReadEpisode(dir); err != nil || episode.Pending || episode.Gen != generation {
		t.Errorf("the episode reads %+v, %v, want generation %d acknowledged", episode, err, generation)
	}
}

// A goblin's question nobody answered stops the acknowledgement that would
// retire it: nothing is retired, no notice is kept and the episode stays
// pending, whatever else the acknowledgement asked for.
func TestAnAcknowledgementRefusedOverAQuestionChangesNothing(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	if _, err := AppendOnce(dir, "done/g1", "notify", "g1", "done: g1"); err != nil {
		t.Fatal(err)
	}
	asked, err := Append(dir, "notify", "g2", "blocked: which way")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := PublishEpisode(dir)
	if err != nil {
		t.Fatal(err)
	}
	before := stateEntries(t, dir)

	// Act
	refused, err := Acknowledge(dir, Ack{Through: &asked.Seq, Generation: &generation})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(refused.Refused) != 1 || refused.Refused[0].Seq != asked.Seq {
		t.Errorf("the acknowledgement refused over %+v, want the one question at %d", refused.Refused, asked.Seq)
	}
	if after := stateEntries(t, dir); !slices.Equal(after, before) {
		t.Errorf("the refused acknowledgement left %v in the state folder, want it as it was: %v", after, before)
	}
	if pending, err := Pending(dir); err != nil || len(pending) != 2 {
		t.Errorf("the queue holds %v, %v, want both records", pending, err)
	}
	if episode, err := ReadEpisode(dir); err != nil || !episode.Pending {
		t.Errorf("the episode reads %+v, %v, want it still pending", episode, err)
	}

	// The CFO read the question: the same acknowledgement retires everything.
	read, err := Acknowledge(dir, Ack{Through: &asked.Seq, Generation: &generation, ShouldRetireQuestions: true})
	if err != nil || len(read.Refused) != 0 || len(read.Pending) != 0 || read.Episode.Pending {
		t.Errorf("the acknowledgement with the questions read left %+v, %v, want an empty queue and the episode acknowledged", read, err)
	}
	if again, isNew, err := AppendFirst(dir, "done/g1", "notify", "g1", "done: g1"); err != nil || isNew {
		t.Errorf("the notice retired with the question was appended again: %+v, new = %t, %v", again, isNew, err)
	}
}
