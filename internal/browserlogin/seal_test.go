package browserlogin

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestASealHoldsUntilItLiftsOrItsTimeRunsOut(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	now := time.Date(2026, 10, 13, 21, 0, 0, 0, time.UTC)

	// Act
	err := Seal(stateDir, "pd-keys", now.Add(2*time.Minute))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		task     string
		at       time.Time
		isSealed bool
	}{
		{"pd-keys", now, true},
		{"pd-keys", now.Add(2 * time.Minute), false},
		{"pd-other", now, false},
	} {
		if isSealed, err := Sealed(stateDir, test.task, test.at); err != nil || isSealed != test.isSealed {
			t.Errorf("Sealed(%s, %s) = %t, %v, want %t", test.task, test.at, isSealed, err, test.isSealed)
		}
	}
	if err := Unseal(stateDir, "pd-keys"); err != nil {
		t.Fatal(err)
	}
	if isSealed, err := Sealed(stateDir, "pd-keys", now); err != nil || isSealed {
		t.Fatalf("after Unseal: %t, %v, want unsealed", isSealed, err)
	}
	if err := Unseal(stateDir, "pd-keys"); err != nil {
		t.Fatalf("a second Unseal = %v, want an unsealed task left as it is", err)
	}
}

func TestADamagedSealStillSealsAndATaskThatIsNoTaskIDIsRefused(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	if err := Seal(stateDir, "pd-keys", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, sealsDir, "pd-keys.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	isSealed, err := Sealed(stateDir, "pd-keys", time.Now())

	// Assert
	if !isSealed || err == nil {
		t.Fatalf("a damaged seal = %t, %v, want sealed with the damage said", isSealed, err)
	}
	for _, task := range []string{"", "../pd-keys", `..\pd-keys`, "pd keys"} {
		if err := Seal(stateDir, task, time.Now().Add(time.Minute)); err == nil {
			t.Errorf("Seal(%q) was accepted", task)
		}
		if _, err := Sealed(stateDir, task, time.Now()); err == nil {
			t.Errorf("Sealed(%q) was answered", task)
		}
	}
}

func TestClearingSealsLiftsEveryOne(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	for _, task := range []string{"pd-keys", "pd-other"} {
		if err := Seal(stateDir, task, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	err := ClearSeals(stateDir)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{"pd-keys", "pd-other"} {
		if isSealed, err := Sealed(stateDir, task, time.Now()); err != nil || isSealed {
			t.Errorf("%s is still sealed: %v", task, err)
		}
	}
	if err := ClearSeals(stateDir); err != nil {
		t.Fatalf("clearing no seals = %v", err)
	}
}
