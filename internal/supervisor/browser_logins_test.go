package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/browserlogin"
)

// declareLogin writes project's manifest with its stripe service and logins.
func declareLogin(t *testing.T, broker *browserBroker, project string, logins ...auth.BrowserLogin) {
	t.Helper()
	path := auth.ManifestPath(broker.data, project)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(auth.Manifest{Project: auth.ProjectName(project), Services: []auth.Service{{Name: "stripe", Method: auth.MethodEnv, Env: []string{"STRIPE_TEST_SECRET_KEY"}}}, Logins: logins})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectsNamingOneAccountShareItsLoginAndOthersKeepTheirOwn(t *testing.T) {
	// Arrange
	broker, _, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	brokerTask(t, broker, "shift-keys", "SIQshift")
	brokerTask(t, broker, "clock-keys", "Clock-In")
	shared := auth.BrowserLogin{Provider: "stripe", Account: "SIQstack"}
	declareLogin(t, broker, "PrecisionDocs-AI", shared)
	declareLogin(t, broker, "SIQshift", shared)

	// Act
	var leases []BrowserLease
	for _, task := range []string{"pd-keys", "shift-keys", "clock-keys"} {
		lease, err := broker.attach(context.Background(), task, "stripe")
		if err != nil {
			t.Fatalf("%s: %v", task, err)
		}
		leases = append(leases, lease)
		if err := broker.release(task, "stripe"); err != nil {
			t.Fatal(err)
		}
		broker.work.Wait()
	}

	// Assert
	if leases[0].Login != "stripe-siqstack" || leases[1].Login != "stripe-siqstack" || leases[0].Account != "SIQstack" {
		t.Fatalf("leases = %+v, want both naming projects on the SIQstack login", leases)
	}
	if leases[2].Login != "stripe-clock-in" || leases[2].Account != "Clock-In" {
		t.Fatalf("lease = %+v, want the project that names no login on its own", leases[2])
	}
	logins := registered(t, broker)
	index := slices.IndexFunc(logins, func(login browserlogin.Login) bool { return login.ID == "stripe-siqstack" })
	if index < 0 || !slices.Equal(logins[index].Projects, []string{"PrecisionDocs-AI", "SIQshift"}) {
		t.Fatalf("registry = %+v, want the shared login listing both projects", logins)
	}
}

func TestNamingAnotherProjectsAccountReachesItsLoginOnlyOnceThatProjectNamesIt(t *testing.T) {
	// Arrange
	broker, _, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "PrecisionDocs-AI")
	brokerTask(t, broker, "clock-keys", "Clock-In")
	if _, err := broker.attach(context.Background(), "pd-keys", "stripe"); err != nil {
		t.Fatal(err)
	}
	if err := broker.release("pd-keys", "stripe"); err != nil {
		t.Fatal(err)
	}
	broker.work.Wait()
	declareLogin(t, broker, "Clock-In", auth.BrowserLogin{Provider: "stripe", Account: "PrecisionDocs-AI"})

	// Act
	_, refused := broker.attach(context.Background(), "clock-keys", "stripe")
	declareLogin(t, broker, "PrecisionDocs-AI", auth.BrowserLogin{Provider: "stripe", Account: "PrecisionDocs-AI"})
	if _, err := broker.attach(context.Background(), "pd-keys", "stripe"); err != nil {
		t.Fatal(err)
	}
	if err := broker.release("pd-keys", "stripe"); err != nil {
		t.Fatal(err)
	}
	broker.work.Wait()
	joined, err := broker.attach(context.Background(), "clock-keys", "stripe")

	// Assert
	if refused == nil || !strings.Contains(refused.Error(), "belongs to another project") {
		t.Fatalf("first attach = %v, want the project's own login refused to the other project", refused)
	}
	if err != nil || joined.Login != "stripe-precisiondocs-ai" {
		t.Fatalf("attach once both name it = %+v, %v, want the shared login", joined, err)
	}
}

func TestAFetchReadsOnTheAccountTheProjectNames(t *testing.T) {
	for _, test := range []struct {
		name, recorded, declared, want string
	}{
		{"his sign-in named no account", "", "acct_declared", "acct_declared"},
		{"his sign-in named another account", "acct_other", "acct_declared", "acct_declared"},
		{"the project names no account", "acct_recorded", "", "acct_recorded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := newFetchBoard(t)
			declareLogin(t, b.broker, "PrecisionDocs-AI", auth.BrowserLogin{Provider: "stripe", Account: "PrecisionDocs", AccountID: test.declared})
			now := time.Now()
			if err := browserlogin.Write(b.broker.state, []browserlogin.Login{{ID: "stripe-precisiondocs", Provider: "stripe", Account: "PrecisionDocs", AccountID: test.recorded, Projects: []string{"PrecisionDocs-AI"}, SignedInAt: &now}}); err != nil {
				t.Fatal(err)
			}

			// Act
			lease, err := b.fetch()
			b.service.credentialWork.Wait()

			// Assert
			if err != nil || len(lease.Fetched) != 2 || len(b.reads) != 1 || b.reads[0].Account != test.want {
				t.Fatalf("lease = %+v, reads = %+v, %v, want the keys read on %s", lease, b.reads, err, test.want)
			}
			if logins := registered(t, b.broker); logins[0].AccountID != test.recorded {
				t.Fatalf("registry = %+v, want his recorded account kept as it was", logins)
			}
		})
	}
}
