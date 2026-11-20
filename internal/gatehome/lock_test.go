package gatehome

import (
	"path/filepath"
	"testing"
)

// IsMoving reads a move's lock without taking it for long: no lock file, or
// one nobody holds, is no move, and one a move holds is a move.
func TestIsMovingSaysWhetherAMoveHoldsTheLock(t *testing.T) {
	for _, test := range []struct {
		name         string
		isMade       bool
		isHeld       bool
		wantIsMoving bool
	}{
		{name: "no lock file", wantIsMoving: false},
		{name: "a lock file nobody holds", isMade: true, wantIsMoving: false},
		{name: "a lock a move holds", isMade: true, isHeld: true, wantIsMoving: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			lock := filepath.Join(t.TempDir(), "gate-home-move.lock")
			if test.isMade {
				unlock, isTaken, err := lockFile(lock)
				if err != nil || !isTaken {
					t.Fatalf("take = %v, %v", isTaken, err)
				}
				if test.isHeld {
					defer unlock()
				} else if err := unlock(); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			isMoving, err := IsMoving(lock)

			// Assert
			if err != nil || isMoving != test.wantIsMoving {
				t.Fatalf("IsMoving = %v, %v; want %v", isMoving, err, test.wantIsMoving)
			}
			if test.isHeld {
				return
			}
			unlock, isTaken, err := lockFile(lock)
			if err != nil || !isTaken {
				t.Fatalf("the lock stayed held after IsMoving: %v, %v", isTaken, err)
			}
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
