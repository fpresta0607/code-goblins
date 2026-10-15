package cirunner

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodSettings = `{"app_id": 1, "installation_id": 2, "owner": "fpresta0607", "repo": "PrecisionDocs-AI", "allowed_sid": "S-1-5-21-1-2-3-1001"}`

// serviceFolder is a folder holding settings and, unless keyType is empty, a
// key of keyType: pkcs1, pkcs8 or ecdsa.
func serviceFolder(t *testing.T, settings, keyType string) (string, *rsa.PrivateKey) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if keyType == "" {
		return dir, nil
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	switch keyType {
	case "pkcs8":
		encoded, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: encoded}
	case "ecdsa":
		other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := x509.MarshalPKCS8PrivateKey(other)
		if err != nil {
			t.Fatal(err)
		}
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: encoded}
	}
	if err := os.WriteFile(filepath.Join(dir, "app-key.pem"), pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, key
}

// The settings and the App's key are read in either form GitHub's keys come
// in.
func TestReadConfigReadsTheSettingsAndTheKey(t *testing.T) {
	for _, keyType := range []string{"pkcs1", "pkcs8"} {
		t.Run(keyType, func(t *testing.T) {
			// Arrange
			dir, want := serviceFolder(t, goodSettings, keyType)

			// Act
			config, key, err := ReadConfig(dir)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			wantConfig := Config{AppID: 1, InstallationID: 2, Owner: "fpresta0607", Repo: "PrecisionDocs-AI", Allowed: "S-1-5-21-1-2-3-1001"}
			if config != wantConfig || !key.Equal(want) {
				t.Errorf("ReadConfig = %+v and a key equal %v; want %+v and the App's key", config, key.Equal(want), wantConfig)
			}
		})
	}
}

// Settings missing, unknown or out of shape, and a key that is not the App's
// RSA key, are refused with what is wrong.
func TestReadConfigRefusesWhatItCannotUse(t *testing.T) {
	for name, row := range map[string]struct {
		settings string
		keyType  string
		want     string
	}{
		"no App id":                 {settings: `{"installation_id": 2, "owner": "o", "repo": "r", "allowed_sid": "S-1-5-21-1"}`, keyType: "pkcs1", want: "no App id"},
		"no installation":           {settings: `{"app_id": 1, "owner": "o", "repo": "r", "allowed_sid": "S-1-5-21-1"}`, keyType: "pkcs1", want: "no installation id"},
		"a repository with a slash": {settings: `{"app_id": 1, "installation_id": 2, "owner": "o", "repo": "r/../x", "allowed_sid": "S-1-5-21-1"}`, keyType: "pkcs1", want: "is not a GitHub owner and repository"},
		"no owner":                  {settings: `{"app_id": 1, "installation_id": 2, "repo": "r", "allowed_sid": "S-1-5-21-1"}`, keyType: "pkcs1", want: "is not a GitHub owner and repository"},
		"an allowed user by name":   {settings: `{"app_id": 1, "installation_id": 2, "owner": "o", "repo": "r", "allowed_sid": "fpres"}`, keyType: "pkcs1", want: "is not a SID"},
		"an unknown setting":        {settings: `{"app_id": 1, "installation_id": 2, "owner": "o", "repo": "r", "allowed_sid": "S-1-5-21-1", "base": "https://example.com"}`, keyType: "pkcs1", want: "unknown field"},
		"no key":                    {settings: goodSettings, want: "read the App's key"},
		"a key that is not RSA":     {settings: goodSettings, keyType: "ecdsa", want: "not an RSA key"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir, _ := serviceFolder(t, row.settings, row.keyType)

			// Act
			_, key, err := ReadConfig(dir)

			// Assert
			if err == nil || key != nil || !strings.Contains(err.Error(), row.want) {
				t.Errorf("ReadConfig = %v; want it refused as %q", err, row.want)
			}
		})
	}
}
