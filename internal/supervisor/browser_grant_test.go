package supervisor

import (
	"context"
	"os"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// browserGrant is a task's credential grant and its project's services: only
// a task whose effective grant holds the declared stripe service may use the
// project's Stripe login.
type browserGrant struct {
	name                                       string
	isStated, isDefault, isDeclared, isAllowed bool
	services                                   []string
}

var browserGrants = []browserGrant{
	{"credentials none", true, true, true, false, nil},
	{"withheld service", true, false, true, false, []string{"github"}},
	{"granted service", true, false, true, true, []string{"stripe"}},
	{"undeclared service", true, false, false, false, []string{"stripe"}},
	{"unstated with default", false, true, true, true, nil},
	{"unstated without default", false, false, true, false, nil},
}

// grantBrowserTask gives task pd-keys of project the grant test names.
func grantBrowserTask(t *testing.T, broker *browserBroker, project string, test browserGrant) {
	t.Helper()
	meta, err := state.ReadTaskMeta(broker.state, "pd-keys")
	if err != nil {
		t.Fatal(err)
	}
	meta.HasCredentials, meta.Credentials = test.isStated, test.services
	if err := state.WriteTaskMeta(broker.state, meta); err != nil {
		t.Fatal(err)
	}
	if test.isDeclared {
		brokerManifest(t, broker, project, auth.Service{Name: "stripe", Method: auth.MethodEnv, Env: []string{"STRIPE_TEST_SECRET_KEY"}, Default: test.isDefault})
	} else {
		brokerManifest(t, broker, project, auth.Service{Name: "github", Method: auth.MethodCLI})
	}
}

func TestBrowserAttachRequiresItsDeclaredServicesEffectiveGrant(t *testing.T) {
	for _, test := range browserGrants {
		t.Run(test.name, func(t *testing.T) {
			broker, chrome, _, _ := testBroker(t)
			brokerTask(t, broker, "pd-keys", "throwaway")
			grantBrowserTask(t, broker, "throwaway", test)

			_, err := broker.attach(context.Background(), "pd-keys", "stripe")

			if (err == nil) != test.isAllowed || (len(chrome.launches) == 1) != test.isAllowed {
				t.Fatalf("attach = %v, launches %d, allowed %t", err, len(chrome.launches), test.isAllowed)
			}
		})
	}
}

func TestBrowserRevocationStopsRenewalAndSweepClosesButReleaseStillWorks(t *testing.T) {
	for _, action := range []string{"release", "sweep"} {
		t.Run(action, func(t *testing.T) {
			broker, chrome, _, _ := testBroker(t)
			brokerTask(t, broker, "pd-keys", "throwaway")
			if _, err := broker.attach(context.Background(), "pd-keys", "stripe"); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteTaskCredentials(broker.state, "pd-keys", nil); err != nil {
				t.Fatal(err)
			}

			if _, err := broker.attach(context.Background(), "pd-keys", "stripe"); err == nil {
				t.Error("a revoked task renewed its browser")
			}
			var err error
			if action == "release" {
				err = broker.release("pd-keys", "stripe")
			} else {
				err = broker.sweep()
			}

			if err != nil {
				t.Fatal(err)
			}
			if len(chrome.endedPIDs(broker)) != 1 {
				t.Fatal("the revoked browser was not closed")
			}
		})
	}
}

func TestBrowserAttachRefusesUnreadableGrantManifest(t *testing.T) {
	broker, chrome, _, _ := testBroker(t)
	brokerTask(t, broker, "pd-keys", "throwaway")
	if err := os.WriteFile(auth.ManifestPath(broker.data, "throwaway"), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := broker.attach(context.Background(), "pd-keys", "stripe")

	if err == nil || len(chrome.launches) != 0 {
		t.Fatal("an unreadable grant launched a browser")
	}
}
