package tickets

import (
	"slices"
	"testing"
)

func TestRememberTeammatesKeepsEveryLoginAndNameOnce(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	if err := RememberTeammates(dir, []Actor{{Login: "jdoe", Name: "Jane Doe"}}); err != nil {
		t.Fatal(err)
	}

	// Act: the same person again in another case, and someone new.
	err := RememberTeammates(dir, []Actor{{Login: "JDOE", Name: " Jane Doe "}, {Login: "rroe"}})
	got, readErr := Teammates(dir)

	// Assert
	if err != nil || readErr != nil {
		t.Fatal(err, readErr)
	}
	if want := []string{"Jane Doe", "jdoe", "rroe"}; !slices.Equal(got, want) {
		t.Fatalf("teammates = %v, want %v", got, want)
	}
}

func TestTeammatesAreNoneBeforeAnyIsRemembered(t *testing.T) {
	got, err := Teammates(t.TempDir())
	if err != nil || got != nil {
		t.Fatalf("Teammates = %v, %v, want none", got, err)
	}
}
