package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Production admission names the one store, in another case exactly where
// the file system that holds the store's cache folder opens a folder by its
// name in another case; elsewhere that spelling is a private store, refused.
func TestAdmissionTakesTheStoreInAnotherCaseOnlyWhereTheFileSystemSaysSo(t *testing.T) {
	// Arrange
	canonical, err := admissionDir("", false)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Dir(filepath.Dir(filepath.Dir(canonical)))
	_, statErr := os.Stat(filepath.Join(filepath.Dir(cache), strings.ToUpper(filepath.Base(cache))))
	isOnePath := statErr == nil
	upper := filepath.Join(cache, "GOBLINS", "VERIFY", "SLOTS")

	// Act
	store, err := admissionDir(upper, false)

	// Assert
	if (err == nil) != isOnePath || err == nil && store != canonical {
		t.Fatalf("admissionDir(%s) = %s, %v; want %s taken: %t, as the file system opens %s in another case: %t", upper, store, err, canonical, isOnePath, cache, isOnePath)
	}
}
