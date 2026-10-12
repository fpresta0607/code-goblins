package browserlogin

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestDefaultIDIsTheProviderAndTheProjectsScopeAsAProfileName(t *testing.T) {
	for _, tc := range []struct{ provider, project, want string }{
		{"stripe", "PrecisionDocs-AI", "stripe-precisiondocs-ai"},
		{"stripe", "Acme.AI", "stripe-acme-ai"},
		{"stripe", "my  shop__v2", "stripe-my-shop-v2"},
		{"stripe", "-edge-", "stripe-edge"},
		{"stripe", strings.Repeat("a", 80), "stripe-" + strings.Repeat("a", 57)},
	} {
		t.Run(tc.project, func(t *testing.T) {
			got := DefaultID(tc.provider, tc.project)
			if got != tc.want || !ValidID(got) {
				t.Fatalf("DefaultID(%q, %q) = %q (valid %v), want %q", tc.provider, tc.project, got, ValidID(got), tc.want)
			}
		})
	}
}

func TestAnIDThatCannotNameAProfileFolderIsRefused(t *testing.T) {
	for _, id := range []string{"", "Stripe", "stripe/x", `..\x`, "stripe-", strings.Repeat("a", 65)} {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

func TestTheRegistryReadsBackWhatWasWrittenAndIsEmptyBeforeAnyLogin(t *testing.T) {
	stateDir := t.TempDir()
	if logins, err := Read(stateDir); err != nil || len(logins) != 0 {
		t.Fatalf("Read before any login = (%v, %v), want none", logins, err)
	}
	used := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	want := []Login{{ID: "stripe-precisiondocs-ai", Provider: "stripe", Account: "PrecisionDocs-AI", Projects: []string{"PrecisionDocs-AI"}, UsedAt: &used,
		Browser: &Browser{PID: 42, Started: used, URL: "http://127.0.0.1:9222", Holder: "pd-keys", Since: used, Hosts: []string{"stripe.com"}}}}

	if err := Write(stateDir, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(stateDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].ID != want[0].ID || !got[0].UsedAt.Equal(used) || got[0].Browser.Holder != "pd-keys" || !slices.Equal(got[0].Projects, want[0].Projects) {
		t.Fatalf("Read = %+v, want %+v", got, want)
	}
}

func TestARegistryHoldingAnIDThatCannotNameAProfileIsRefused(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(RegistryPath(stateDir), []byte(`{"logins":[{"id":"..\\..\\x"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Read(stateDir); err == nil || !strings.Contains(err.Error(), "cannot name a profile") {
		t.Fatalf("Read = %v, want it refused", err)
	}
}

func TestALoginsProfileIsReadableByItsOwnerAlone(t *testing.T) {
	stateDir := t.TempDir()

	path, err := MakeProfile(stateDir, "stripe-precisiondocs-ai")
	if err != nil {
		t.Fatal(err)
	}

	if path != filepath.Join(stateDir, "browser-logins", "stripe-precisiondocs-ai") {
		t.Fatalf("profile = %s, want it under state/browser-logins", path)
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sddl := descriptor.String()
	if control&windows.SE_DACL_PROTECTED == 0 || strings.Count(sddl, "(A;") != 1 || !strings.Contains(sddl, user.User.Sid.String()) {
		t.Fatalf("profile DACL = %s, want one protected entry for the current user", sddl)
	}
	// Making it again keeps it.
	if again, err := MakeProfile(stateDir, "stripe-precisiondocs-ai"); err != nil || again != path {
		t.Fatalf("MakeProfile again = (%s, %v), want the same folder", again, err)
	}
}

func TestChromeOnALoginIsHeadlessAndReachesOnlyItsHosts(t *testing.T) {
	launch := Launch{Executable: `C:\chrome.exe`, Profile: `C:\cfo\state\browser-logins\stripe-x`, Hosts: []string{"stripe.com", "*.stripe.com"}, Headless: true}

	arguments := launch.Arguments()

	for _, want := range []string{`--user-data-dir=C:\cfo\state\browser-logins\stripe-x`, "--remote-debugging-port=0", "--headless", "--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE stripe.com, EXCLUDE *.stripe.com"} {
		if !slices.Contains(arguments, want) {
			t.Errorf("arguments %q lack %q", arguments, want)
		}
	}
}
