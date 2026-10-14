package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/keypages"
)

// lendToTheGoblin lends the board's login to its goblin pd-keys, and has the
// stand-in key page note whether pd-keys was sealed while it was read.
func lendToTheGoblin(t *testing.T, b *fetchBoard) (BrowserLease, *bool) {
	t.Helper()
	b.signedIn("acct_fixture")
	lease, err := b.broker.attach(context.Background(), "pd-keys", "stripe")
	if err != nil {
		t.Fatal(err)
	}
	isSealed := false
	read := b.broker.readKeys
	b.broker.readKeys = func(ctx context.Context, url string, provider keypages.Provider, account string, keys []keypages.Key) (map[string]string, error) {
		isSealed, _ = browserlogin.Sealed(b.broker.state, "pd-keys", *b.now)
		return read(ctx, url, provider, account, keys)
	}
	return lease, &isSealed
}

func TestAFetchInTheGoblinsOwnBrowserSealsItOnlyWhileItReads(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	lease, isSealedDuringRead := lendToTheGoblin(t, b)

	// Act
	fetched, err := b.fetch()
	b.service.credentialWork.Wait()

	// Assert
	if err != nil || len(fetched.Fetched) != 2 {
		t.Fatalf("fetch = %+v, %v, want both keys stored", fetched, err)
	}
	if !*isSealedDuringRead {
		t.Fatal("the goblin's browser was read in with no seal on its browser commands")
	}
	if isSealed, err := browserlogin.Sealed(b.broker.state, "pd-keys", *b.now); err != nil || isSealed {
		t.Fatalf("after the fetch the seal = %t, %v, want it lifted", isSealed, err)
	}
	if len(b.reads) != 1 || b.reads[0].URL != lease.URL || len(b.chrome.launches) != 1 || len(b.chrome.endedPIDs(b.broker)) != 0 {
		t.Fatalf("reads = %+v, launches = %d, want the read in the goblin's own browser, left running", b.reads, len(b.chrome.launches))
	}
	if logins := registered(t, b.broker); logins[0].Browser == nil || logins[0].Browser.Holder != "pd-keys" {
		t.Fatalf("registry = %+v, want the browser still lent to pd-keys", logins)
	}
	for name, value := range b.page {
		if got, found := b.vaultValue(name); !found || got != value {
			t.Fatalf("%s did not reach the vault", name)
		}
	}
}

func TestAFetchInTheGoblinsOwnBrowserLiftsItsSealWhenTheReadFails(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	_, isSealedDuringRead := lendToTheGoblin(t, b)
	b.pageErr = errors.New("the page changed")

	// Act
	_, err := b.fetch()

	// Assert
	if err == nil || !*isSealedDuringRead {
		t.Fatalf("fetch = %v, sealed %t, want a sealed read that failed", err, *isSealedDuringRead)
	}
	if isSealed, err := browserlogin.Sealed(b.broker.state, "pd-keys", *b.now); err != nil || isSealed {
		t.Fatalf("after the failed fetch the seal = %t, %v, want it lifted", isSealed, err)
	}
	if logins := registered(t, b.broker); logins[0].Browser == nil {
		t.Fatal("the failed fetch took the goblin's browser from it")
	}
}

func TestAStartingSupervisorLiftsASealAStoppedOneLeft(t *testing.T) {
	// Arrange
	b := newFetchBoard(t)
	if err := browserlogin.Seal(b.broker.state, "pd-keys", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	b.service.keepBrowsers(ctx)

	// Assert
	if isSealed, err := browserlogin.Sealed(b.broker.state, "pd-keys", time.Now()); err != nil || isSealed {
		t.Fatalf("the seal = %t, %v, want it lifted at the start", isSealed, err)
	}
}
