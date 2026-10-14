package supervisor

import (
	"context"
	"os"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestBrowserAttachRequiresItsDeclaredServicesEffectiveGrant(t *testing.T) {
	for _, test := range []struct {
		name                                       string
		isStated, isDefault, isDeclared, isAllowed bool
		services                                   []string
	}{
		{"credentials none", true, true, true, false, nil},
		{"withheld service", true, false, true, false, []string{"github"}},
		{"granted service", true, false, true, true, []string{"stripe"}},
		{"undeclared service", true, false, false, false, []string{"stripe"}},
		{"unstated with default", false, true, true, true, nil},
		{"unstated without default", false, false, true, false, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			broker, chrome, _, _ := testBroker(t)
			brokerTask(t, broker, "pd-keys", "throwaway")
			meta, err := state.ReadTaskMeta(broker.state, "pd-keys")
			if err != nil {
				t.Fatal(err)
			}
			meta.HasCredentials, meta.Credentials = test.isStated, test.services
			if err := state.WriteTaskMeta(broker.state, meta); err != nil {
				t.Fatal(err)
			}
			if test.isDeclared {
				brokerManifest(t, broker, "throwaway", auth.Service{Name: "stripe", Method: auth.MethodEnv, Env: []string{"STRIPE_TEST_SECRET_KEY"}, Default: test.isDefault})
			} else {
				brokerManifest(t, broker, "throwaway", auth.Service{Name: "github", Method: auth.MethodCLI})
			}

			_, err = broker.attach(context.Background(), "pd-keys", "stripe")

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
