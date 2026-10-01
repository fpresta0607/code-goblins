package auth

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeRawManifest(t *testing.T, dataDir, project, body string) {
	t.Helper()
	path := ManifestPath(dataDir, project)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A manifest's format hints say what a credential's value should look like,
// so the credential card can warn about a pasted key of the wrong kind.
func TestFormatHintsMatchNamesExactlyOrByPrefix(t *testing.T) {
	// Arrange
	dataDir := t.TempDir()
	writeRawManifest(t, dataDir, "shop", `{"project":"shop","services":[{"name":"stripe","method":"env","env":["STRIPE_SECRET_KEY"]}],
		"formats":[
			{"names":["STRIPE_*"],"prefixes":["sk_","rk_"],"warn":[{"prefix":"sk_live_","say":"A live secret key can do anything on the account; use a restricted rk_live_ key."}]},
			{"names":["RESEND_API_KEY"],"prefixes":["re_"]}
		]}`)

	// Act
	manifest, err := LoadManifest(dataDir, "shop")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	stripe, stripeFound := manifest.FormatFor("STRIPE_WEBHOOK_SECRET")
	resend, resendFound := manifest.FormatFor("RESEND_API_KEY")
	_, otherFound := manifest.FormatFor("RESEND_API_KEY_2")

	// Assert
	if !stripeFound || !slices.Equal(stripe.Prefixes, []string{"sk_", "rk_"}) || len(stripe.Warn) != 1 || stripe.Warn[0].Prefix != "sk_live_" || !strings.Contains(stripe.Warn[0].Say, "restricted") {
		t.Errorf("STRIPE_WEBHOOK_SECRET hint = %+v, %v; want the STRIPE_* prefixes and its sk_live_ warning", stripe, stripeFound)
	}
	if !resendFound || !slices.Equal(resend.Prefixes, []string{"re_"}) || len(resend.Warn) != 0 {
		t.Errorf("RESEND_API_KEY hint = %+v, %v; want the exact name's prefix", resend, resendFound)
	}
	if otherFound {
		t.Error("an exact name matched a longer name")
	}
}

func TestFormatHintsThatCannotWarnAboutAnythingAreRefused(t *testing.T) {
	for name, formats := range map[string]string{
		"a pattern with a star inside":   `[{"names":["STRIPE*KEY"],"prefixes":["sk_"]}]`,
		"no names":                       `[{"names":[],"prefixes":["sk_"]}]`,
		"neither prefixes nor warnings":  `[{"names":["STRIPE_*"]}]`,
		"an empty prefix":                `[{"names":["STRIPE_*"],"prefixes":[""]}]`,
		"a warning with nothing to say":  `[{"names":["STRIPE_*"],"warn":[{"prefix":"sk_live_","say":" "}]}]`,
		"a warning with no prefix":       `[{"names":["STRIPE_*"],"warn":[{"prefix":"","say":"Use a restricted key."}]}]`,
		"a field the hint does not know": `[{"names":["STRIPE_*"],"prefixes":["sk_"],"suffixes":["_x"]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dataDir := t.TempDir()
			writeRawManifest(t, dataDir, "shop", `{"project":"shop","services":[],"formats":`+formats+`}`)

			// Act
			_, err := LoadManifest(dataDir, "shop")

			// Assert
			if err == nil {
				t.Fatal("LoadManifest took a format hint that cannot work")
			}
		})
	}
}
