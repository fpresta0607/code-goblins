package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// failingStore is a store whose saves fail until its state directory is
// made, and the failure its first save met.
func failingStore(t *testing.T) (*Store, error) {
	t.Helper()
	store := &Store{Home: home.Home{State: filepath.Join(t.TempDir(), "state")}}
	err := store.save()
	if !errors.Is(err, ErrStorage) {
		t.Fatalf("a save into a missing directory returned %v, want a storage failure", err)
	}
	return store, err
}

// The board showed the Overlord "supervisor persistence failed: rename
// ...\.cfo-tmp-2713629804 ...\.supervisor.json: Access is denied" on
// 2026-10-01 for a lock that was gone within the minute. A failed save
// loses nothing: what it would have saved stays where it came from and the
// next cycle, at most two seconds away, saves it. So one failure is not the
// Overlord's business; only a store that keeps failing is.
func TestATransientPersistFailureStaysOffTheBoard(t *testing.T) {
	unrelated := errors.New("questions inbox unreadable")
	tests := []struct {
		name    string
		publish func(failure error) error
		want    string
	}{
		{name: "a storage failure alone", publish: func(failure error) error { return failure }, want: ""},
		{name: "a storage failure beside another error", publish: func(failure error) error { return errors.Join(failure, unrelated) }, want: unrelated.Error()},
		{name: "another error alone", publish: func(error) error { return unrelated }, want: unrelated.Error()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, failure := failingStore(t)
			s := &Service{Store: store}

			// Act
			s.publish(test.publish(failure))

			// Assert
			if got := s.lastError; got != test.want {
				t.Errorf("the board shows %q, want %q", got, test.want)
			}
		})
	}
}

// A store that keeps failing past storageGrace since its first failed save
// is the Overlord's business, whatever else was published in between.
func TestAPersistFailureThatKeepsFailingReachesTheBoard(t *testing.T) {
	tests := []struct {
		name    string
		between []error
	}{
		{name: "with nothing published in between"},
		{name: "with unrelated errors published in between", between: []error{errors.New("page poll failed"), errors.New("run pipe closed")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := failingStore(t)
			s := &Service{Store: store}
			store.failingSince.Store(time.Now().Add(-storageGrace - time.Second).UnixNano())

			// Act
			for _, err := range test.between {
				s.publish(err)
				if err := store.save(); !errors.Is(err, ErrStorage) {
					t.Fatalf("a save into a missing directory returned %v, want a storage failure", err)
				}
			}
			failure := store.save()
			s.publish(failure)

			// Assert
			if s.lastError != failure.Error() {
				t.Errorf("after failing past the grace the board shows %q, want %q", s.lastError, failure.Error())
			}
		})
	}
}

// A successful save starts the grace over, whatever the cycle that made it
// published, so a later one-off failure stays off the board.
func TestASuccessfulSaveStartsTheGraceOver(t *testing.T) {
	tests := []struct {
		name      string
		published error
		want      string
	}{
		{name: "a clean cycle", published: nil, want: ""},
		{name: "a cycle that met only an unrelated error", published: errors.New("reconcile failed"), want: "reconcile failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := failingStore(t)
			s := &Service{Store: store}
			store.failingSince.Store(time.Now().Add(-storageGrace - time.Second).UnixNano())
			if err := os.Mkdir(store.Home.State, 0700); err != nil {
				t.Fatal(err)
			}
			if err := store.save(); err != nil {
				t.Fatalf("a save into an existing directory failed: %v", err)
			}
			s.publish(test.published)
			if err := os.RemoveAll(store.Home.State); err != nil {
				t.Fatal(err)
			}

			// Act
			s.publish(errors.Join(store.save(), test.published))

			// Assert
			if s.lastError != test.want {
				t.Errorf("a failure right after a successful save left the board showing %q, want %q", s.lastError, test.want)
			}
		})
	}
}
