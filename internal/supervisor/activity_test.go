package supervisor

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

func TestPresentationAndActivityReceiptsStayBoundedAndDoNotDriveWork(t *testing.T) {
	store, h := testStore(t)
	now := time.Now().UTC()
	if err := store.Accept(event(t, h, "SessionStart", "worker", "", now)); err != nil {
		t.Fatal(err)
	}
	node := store.Snapshot().TaskSessions["task-1"]
	a := BoardActivity{ID: "browser-1", Kind: "browser", TaskID: "task-1", Generation: "g1", Target: node, State: "active", URL: "http://127.0.0.1:5173/", At: now, Until: now.Add(time.Minute)}
	if err := store.acceptActivity(a); err != nil {
		t.Fatal(err)
	}
	if err := store.acceptActivity(a); err != nil {
		t.Fatal(err)
	}
	if len(store.Snapshot().Activity) != 2 {
		t.Fatal("receipt was duplicated or start lost")
	}
	for _, url := range []string{"javascript:alert(1)", "https://user:pass@example.com/", "https://example.com/?token=secret", "https://example.com/#secret", "file:///C:/secret", "http://example.com/", "https://example.com/github_pat_abcdef"} {
		bad := a
		bad.ID = "unsafe-url"
		bad.URL = url
		if err := store.acceptActivity(bad); err == nil {
			t.Fatal("unsafe presentation URL accepted", url)
		}
	}
	bad := a
	bad.ID = "stale-task"
	bad.Generation = "previous"
	if err := store.acceptActivity(bad); err == nil {
		t.Fatal("stale task activity accepted")
	}
	a.State = "ended"
	a.At = now.Add(time.Second)
	if err := store.acceptActivity(a); err != nil {
		t.Fatal(err)
	}
	a.State = "active"
	a.At = now.Add(2 * time.Second)
	if err := store.acceptActivity(a); err == nil {
		t.Fatal("finished presentation reopened")
	}
	for i := 0; i < 300; i++ {
		a.ID = fmt.Sprintf("send-%03d", i)
		a.Kind = "message"
		a.URL = ""
		a.State = "accepted"
		a.At = now
		a.Until = time.Time{}
		if err := store.acceptActivity(a); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.Snapshot().Activity) > maxBoardActivity || len(store.Snapshot().Actions) != 0 {
		t.Fatal("presentation changed work or unbounded history")
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Snapshot().Activity) != maxBoardActivity {
		t.Fatal("receipts did not survive restart")
	}
	if err := reopened.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
		t.Fatal("view choice executed work")
		return Evaluation{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(reopened.Snapshot().Activity), "token=secret") {
		t.Fatal("invalid URL persisted")
	}
}

func TestPendingPresentationPreservesEndIdentityAndOrdering(t *testing.T) {
	store, h := testStore(t)
	now := time.Now().UTC()
	if err := store.Accept(event(t, h, "SessionStart", "worker", "", now)); err != nil {
		t.Fatal(err)
	}
	a := BoardActivity{ID: "pending-review", Kind: "review", TaskID: "task-1", Generation: "g1", Target: store.Snapshot().TaskSessions["task-1"], State: "active", URL: "http://127.0.0.1:4387/session/example", At: now, Until: now.Add(time.Minute)}
	if err := spoolActivity(h.State, a); err != nil {
		t.Fatal(err)
	}
	newer := a
	newer.At = now.Add(time.Second)
	if err := spoolActivity(h.State, newer); err != nil {
		t.Fatal(err)
	}
	if err := spoolActivity(h.State, a); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []BoardActivity{func() BoardActivity { b := newer; b.URL = "https://example.com/other"; return b }(), func() BoardActivity { b := newer; b.Generation = "g2"; return b }()} {
		if err := spoolActivity(h.State, changed); err == nil {
			t.Fatal("pending ID changed identity")
		}
	}
	ended := newer
	ended.State = "ended"
	ended.At = now.Add(2 * time.Second)
	if err := spoolActivity(h.State, ended); err != nil {
		t.Fatal(err)
	}
	newer.At = now.Add(3 * time.Second)
	if err := spoolActivity(h.State, newer); err == nil {
		t.Fatal("pending end reopened before ingest")
	}
	if err := store.ingestActivity(); err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot().Activity[1]
	if got.State != "ended" || !got.At.Equal(ended.At) {
		t.Fatal("pending terminal receipt lost", got)
	}
}

// Lavish hands out its tailnet name; plain http on a name that resolves only
// to this machine is linked by its loopback form, and every refusal names the
// rule it broke.
func TestPresentationURLNamesTheRuleAndLinksThisMachineByLoopback(t *testing.T) {
	ctx := context.Background()
	for raw, rule := range map[string]string{
		"http://192.0.2.10:4387/session/f26e":  "plain http",
		"https://user:pass@example.com/review": "credentials",
		"https://example.com/review?x=1":       "query",
		"https://example.com/reset-token/x":    "credential (token)",
		"review page":                          "absolute URL",
	} {
		if _, err := PresentationURL(ctx, raw); err == nil || !strings.Contains(err.Error(), rule) {
			t.Errorf("%s: err = %v, want the %q rule named", raw, err, rule)
		}
	}
	for _, raw := range []string{"https://example.com/review", "http://localhost:4387/session/f26e"} {
		if got, err := PresentationURL(ctx, raw); err != nil || got != raw {
			t.Errorf("%s = %q %v, want it unchanged", raw, got, err)
		}
	}
	local := ""
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok && !network.IP.IsLoopback() && network.IP.To4() != nil {
			local = network.IP.String()
			break
		}
	}
	if local == "" {
		t.Skip("this machine has no non-loopback IPv4 address to stand in for its tailnet name")
	}
	if got, err := PresentationURL(ctx, "http://"+local+":4387/session/f26e1c33babf6415"); err != nil || got != "http://127.0.0.1:4387/session/f26e1c33babf6415" {
		t.Fatalf("this machine's own address = %q %v, want its loopback form", got, err)
	}
}

// Serve holds the activity lock while it ingests, every few seconds, so a
// report made at that moment waits for it instead of being refused.
func TestSpoolActivityWaitsForTheIngestLock(t *testing.T) {
	_, h := testStore(t)
	if _, err := lock.AcquireExclusiveNamed(h.State, ".board-activity.lock"); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		released <- lock.ReleaseExclusiveNamed(h.State, ".board-activity.lock")
	}()
	now := time.Now().UTC()
	if err := spoolActivity(h.State, BoardActivity{ID: "held-lock-review", Kind: "review", TaskID: "task-1", Generation: "g1", State: "active", URL: "http://127.0.0.1:4387/session/f26e", At: now, Until: now.Add(time.Minute)}); err != nil {
		t.Fatalf("a report was refused while serve briefly held the lock: %v", err)
	}
	if err := <-released; err != nil {
		t.Fatal(err)
	}
}
