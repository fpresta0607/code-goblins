package host

import (
	"os"
	"reflect"
	"testing"
	"time"
)

// A terminal is given a new proof value each time it is started, resumed or
// switched, and what it started under an earlier one is still its own. The
// record names only the host that runs now, so the terminal's proofs keep
// every digest it was given since the machine started.
func TestATerminalsProofsKeepEveryDigestItWasGivenSinceTheMachineStarted(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	boot := time.Date(2026, 10, 9, 14, 4, 17, 0, time.UTC)
	first, second := ProofSum("first-value"), ProofSum("second-value")

	// Act
	if err := keepProof(stateDir, "g1", first, boot.Add(time.Hour), boot); err != nil {
		t.Fatal(err)
	}
	if err := keepProof(stateDir, "g1", second, boot.Add(2*time.Hour), boot); err != nil {
		t.Fatal(err)
	}
	proofs, err := Proofs(stateDir, "g1")

	// Assert
	if err != nil || !reflect.DeepEqual(proofs, []string{first, second}) {
		t.Fatalf("proofs = %v, %v; want both digests in the order they were given", proofs, err)
	}
	if other, err := Proofs(stateDir, "g2"); err != nil || len(other) != 0 {
		t.Fatalf("another terminal's proofs = %v, %v; want none", other, err)
	}
}

// A host that a build before these proofs started kept none, and what its
// terminal started carries its value still. Its record holds the digest, so
// the terminal's proofs count the record's too.
func TestATerminalsProofsCountItsRecordsDigest(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	running := ProofSum("given-before-proofs-were-kept")
	if err := writeRecord(stateDir, Record{ID: "g1", Pipe: `\\.\pipe\g1`, Token: "token", HostPID: os.Getpid(), ProofSum: running}); err != nil {
		t.Fatal(err)
	}

	// Act
	proofs, err := Proofs(stateDir, "g1")

	// Assert
	if err != nil || !reflect.DeepEqual(proofs, []string{running}) {
		t.Fatalf("proofs = %v, %v; want the digest in the terminal's record", proofs, err)
	}
}

// No process outlives the machine, so a digest from before it started proves
// nothing any more and leaves the terminal's proofs the next time one is kept.
func TestATerminalsProofsDropTheDigestsFromBeforeTheMachineStarted(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	lastBoot := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	boot := time.Date(2026, 10, 9, 14, 4, 17, 0, time.UTC)
	stale, fresh := ProofSum("before-the-restart"), ProofSum("after-the-restart")
	if err := keepProof(stateDir, "g1", stale, lastBoot.Add(time.Hour), lastBoot); err != nil {
		t.Fatal(err)
	}

	// Act
	if err := keepProof(stateDir, "g1", fresh, boot.Add(time.Minute), boot); err != nil {
		t.Fatal(err)
	}
	proofs, err := Proofs(stateDir, "g1")

	// Assert
	if err != nil || !reflect.DeepEqual(proofs, []string{fresh}) {
		t.Fatalf("proofs = %v, %v; want only the digest given since the machine started", proofs, err)
	}
}

// Proofs that cannot be read are an error to whoever reads them, and are
// started over by the next terminal, which starts whatever became of them.
func TestATerminalStartsOverProofsThatCannotBeRead(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	boot := time.Date(2026, 10, 9, 14, 4, 17, 0, time.UTC)
	if err := keepProof(stateDir, "g1", ProofSum("first-value"), boot.Add(time.Hour), boot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proofsPath(stateDir, "g1"), []byte("not a digest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := ProofSum("second-value")

	// Act
	_, unreadable := Proofs(stateDir, "g1")
	keepErr := keepProof(stateDir, "g1", fresh, boot.Add(2*time.Hour), boot)
	proofs, err := Proofs(stateDir, "g1")

	// Assert
	if unreadable == nil {
		t.Error("unreadable proofs were read without an error")
	}
	if keepErr != nil || err != nil || !reflect.DeepEqual(proofs, []string{fresh}) {
		t.Fatalf("after the next terminal: keep %v, proofs %v, %v; want only its own digest", keepErr, proofs, err)
	}
}

// A terminal's proofs refuse an id that is no task's, as its record does, so
// no caller reads a file outside the hosts folder through them.
func TestATerminalsProofsRefuseAnIDThatIsNoTasks(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()

	// Act
	_, readErr := Proofs(stateDir, `..\elsewhere`)
	keepErr := keepProof(stateDir, `..\elsewhere`, ProofSum("value"), time.Now(), time.Now().Add(-time.Hour))

	// Assert
	if readErr == nil || keepErr == nil {
		t.Fatalf("read error %v, keep error %v; want both refused", readErr, keepErr)
	}
}
