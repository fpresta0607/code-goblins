//go:build windows

package auth

import (
	"strings"
	"testing"
)

// selfTestKey is written to the real vault under the cfo: namespace. There is
// no delete path in this package, so the round trip deliberately uses one
// stable, obviously-named entry rather than leaving a trail of new ones.
const selfTestKey = "CFO_CREDENTIAL_SELFTEST"

func TestCredentialManagerRoundTripsThroughTheRealVault(t *testing.T) {
	store, err := openCredentialManager()
	if err != nil {
		t.Skipf("Windows Credential Manager unavailable: %v", err)
	}

	// A value long enough to exercise the blob pointer and size fields, with
	// non-ASCII to prove the UTF-8 blob survives the UTF-16 target marshaling.
	want := "sk_live_" + strings.Repeat("0123456789", 8) + "_ünïcode"
	if err := store.Set(selfTestKey, want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, found, err := store.Get(selfTestKey)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("Get reported the credential missing immediately after Set")
	}
	if got != want {
		t.Fatalf("Get = %q, want %q", got, want)
	}

	keys, err := store.Keys()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if !contains(keys, selfTestKey) {
		t.Errorf("Keys() = %v, want it to include %q", keys, selfTestKey)
	}
	// Enumeration must not leak entries belonging to other applications.
	for _, key := range keys {
		if !ValidEnvName(key) {
			t.Errorf("Keys() returned %q, which is not one of cfo's credential names", key)
		}
	}
}

func TestCredentialManagerReportsAnUnknownKeyAsAbsentNotAnError(t *testing.T) {
	store, err := openCredentialManager()
	if err != nil {
		t.Skipf("Windows Credential Manager unavailable: %v", err)
	}
	value, found, err := store.Get("CFO_CREDENTIAL_THAT_DOES_NOT_EXIST")
	if err != nil {
		t.Fatalf("Get(absent) = error %v, want absence reported as an ordinary state", err)
	}
	if found || value != "" {
		t.Errorf("Get(absent) = (%q, %v), want (\"\", false)", value, found)
	}
}

func TestCredentialManagerRefusesAKeyThatIsNotAnEnvironmentName(t *testing.T) {
	store := credentialManagerStore{}
	if err := store.Set("bad key", "value"); err == nil {
		t.Error("Set with an invalid name = nil, want a refusal")
	}
	if _, _, err := store.Get("bad key"); err == nil {
		t.Error("Get with an invalid name = nil, want a refusal")
	}
}
