package verify

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTurnKeepsCustodyUntilItsWorkersAreConfirmedGone(t *testing.T) {
	// Arrange
	admission := admission(t)
	turn, err := admission.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	guard, err := turn.Protect("controlled-run", "")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	turn.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	other, err := admission.Wait(ctx)
	other.Release()

	// Assert
	if err == nil {
		t.Fatal("released a turn while its workers were unproven")
	}
	if err := guard.Complete(); err != nil {
		t.Fatal(err)
	}
	turn.Release()
	other, err = admission.Wait(context.Background())
	defer other.Release()
	if err != nil {
		t.Fatalf("confirmed cleanup did not release custody: %v", err)
	}
}

func TestTurnReleaseReportsFailureAndRemainsRetryable(t *testing.T) {
	// Arrange
	requested := admission(t)
	turn, err := requested.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Release()
	card := filepath.Join(requested.Dir, "slot-1.run")
	if err := os.Remove(card); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(card, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(card, "occupied"), nil, 0600); err != nil {
		t.Fatal(err)
	}

	// Act
	firstError := turn.Release()
	if err := os.Remove(filepath.Join(card, "occupied")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(card); err != nil {
		t.Fatal(err)
	}
	secondError := turn.Release()

	// Assert
	if firstError == nil || secondError != nil {
		t.Fatalf("release errors = %v then %v", firstError, secondError)
	}
	if _, err := os.Stat(filepath.Join(requested.Dir, "slot-1")); !os.IsNotExist(err) {
		t.Fatalf("successful retry left a held turn: %v", err)
	}
}

func TestProtectionCannotRacePastTurnRelease(t *testing.T) {
	for attempt := 0; attempt < 16; attempt++ {
		admission := admission(t)
		turn, err := admission.Wait(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var guard Custody
		var protectionError error
		var group sync.WaitGroup
		group.Add(2)
		go func() { defer group.Done(); guard, protectionError = turn.Protect("racing-run", "") }()
		go func() { defer group.Done(); turn.Release() }()
		group.Wait()
		if protectionError == nil {
			if !admission.isHeldAs("slot-1", turn.Token()) {
				t.Fatal("worker protection arrived after slot release")
			}
			if err := guard.Complete(); err != nil {
				t.Fatal(err)
			}
		}
		turn.Release()
	}
}

func TestWorkerCustodyRejectsInvalidOrDuplicateRunIDs(t *testing.T) {
	admission := admission(t)
	turn, err := admission.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Release()
	for _, runID := range []string{"", "..", "../outside", `..\outside`, "drive:name", "has space"} {
		if _, err := turn.Protect(runID, ""); err == nil {
			t.Fatalf("protected invalid ID %q", runID)
		}
	}
	guard, err := turn.Protect("same-run", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Protect("same-run", ""); err == nil {
		t.Fatal("replaced existing custody")
	}
	altered := guard
	altered.Token = "other-token"
	if err := altered.Complete(); err == nil {
		t.Fatal("removed another token's custody")
	}
	altered.RunID = "../outside"
	if err := altered.Complete(); err == nil {
		t.Fatal("accepted a path as an execution ID")
	}
	if err := guard.Complete(); err != nil {
		t.Fatal(err)
	}
}

func TestAdmissionRefusesOrphanedOrUnreadableWorkerCustody(t *testing.T) {
	for _, contents := range []string{`{"token":"slot-1:999999:1","run_id":"orphan"}`, `{"token":`} {
		t.Run(contents, func(t *testing.T) {
			// Arrange
			admission := admission(t)
			if err := os.MkdirAll(filepath.Join(admission.Dir, "custody"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(admission.Dir, "custody", "orphan.json"), []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			// Act
			turn, err := admission.Wait(ctx)
			defer turn.Release()

			// Assert
			if err == nil || turn.Token() != "" {
				t.Fatalf("unproven custody admitted another run: %+v, %v", turn, err)
			}
		})
	}
}

func TestNestedTurnsKeepEachWorkersCustodySeparate(t *testing.T) {
	admission := admission(t)
	outer, err := admission.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer outer.Release()
	first, err := outer.Protect("outer", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(TurnVariable, outer.Token())
	inner, err := admission.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := inner.Protect("inner", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Complete(); err != nil {
		t.Fatal(err)
	}
	inner.Release()
	outer.Release()
	if !admission.isHeldAs("slot-1", outer.Token()) {
		t.Fatal("outer release abandoned inner workers")
	}
	if err := second.Complete(); err != nil {
		t.Fatal(err)
	}
	outer.Release()
	if admission.isHeldAs("slot-1", outer.Token()) {
		t.Fatal("empty turn stayed held")
	}
}
