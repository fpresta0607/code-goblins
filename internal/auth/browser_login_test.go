package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAManifestNamesTheBrowserLoginsItsGoblinsFetchWith(t *testing.T) {
	// Arrange
	dataDir := t.TempDir()
	path := ManifestPath(dataDir, "PrecisionDocs-AI")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"project":"PrecisionDocs-AI","services":[{"name":"stripe","method":"env","env":["STRIPE_TEST_SECRET_KEY"]}],` +
		`"logins":[{"provider":"stripe","account":"PrecisionDocs","account_id":"acct_1Fixture","signs_out_after_fetch":true}]}`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	loaded, err := LoadManifest(dataDir, "PrecisionDocs-AI")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	login, found := loaded.LoginFor("stripe")
	if !found || login != (BrowserLogin{Provider: "stripe", Account: "PrecisionDocs", AccountID: "acct_1Fixture", SignsOutAfterFetch: true}) {
		t.Fatalf("LoginFor(stripe) = %+v, %t, want the declared login", login, found)
	}
	if _, found := loaded.LoginFor("github"); found {
		t.Fatal("a provider the manifest names no login for has one")
	}
}

func TestAManifestRefusesALoginAFetchCouldNotUseOrCouldConfuse(t *testing.T) {
	for _, test := range []struct {
		name   string
		logins []BrowserLogin
	}{
		{"no provider", []BrowserLogin{{Account: "PrecisionDocs"}}},
		{"a provider no recipe could name", []BrowserLogin{{Provider: "Stripe!", Account: "PrecisionDocs"}}},
		{"no account", []BrowserLogin{{Provider: "stripe"}}},
		{"an account no folder could be named for", []BrowserLogin{{Provider: "stripe", Account: "../PrecisionDocs"}}},
		{"an overlong account", []BrowserLogin{{Provider: "stripe", Account: strings.Repeat("a", 65)}}},
		{"an account ID no address holds", []BrowserLogin{{Provider: "stripe", Account: "PrecisionDocs", AccountID: "acct/other"}}},
		{"two logins of one provider", []BrowserLogin{{Provider: "stripe", Account: "PrecisionDocs"}, {Provider: "stripe", Account: "SIQstack"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			err := Manifest{Project: "PrecisionDocs-AI", Logins: test.logins}.Validate()

			// Assert
			if err == nil {
				t.Fatal("the login was accepted")
			}
		})
	}
}
