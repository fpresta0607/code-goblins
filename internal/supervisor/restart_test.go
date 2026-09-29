package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

func resumeFixture(t *testing.T) (*CFORecovery, string) {
	t.Helper()
	dir := t.TempDir()
	primary := primaryRegistration{Agent: "claude", Host: "cfo", Process: lock.Info{PID: 1234, Start: time.Now(), Session: "abc01234-5678-9012-abcd-012345678901"}}
	data, err := json.Marshal(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	recovery := &CFORecovery{State: dir, Memory: func() (uint64, uint64, error) { return 8 << 30, 32 << 30, nil }}
	record, err := recovery.Preview()
	if err != nil {
		t.Fatal(err)
	}
	return recovery, record.Identity
}

func TestRestartValidatesIdentityAndMemoryBeforeStopping(t *testing.T) {
	for _, test := range []struct {
		name     string
		identity string
		memory   uint64
	}{
		{"stale", "stale", 8 << 30}, {"low memory", "", 3 << 30},
	} {
		t.Run(test.name, func(t *testing.T) {
			recovery, identity := resumeFixture(t)
			if test.identity != "" {
				identity = test.identity
			}
			recovery.Memory = func() (uint64, uint64, error) { return test.memory, 32 << 30, nil }
			recovery.Restart = func(context.Context, CFOResume) error { t.Fatal("stopped CFO before validation"); return nil }
			if err := recovery.Run(context.Background(), identity); err == nil {
				t.Fatal("accepted unsafe restart")
			}
		})
	}
}

func TestRestartKeepsExactConversationAndRefusesConcurrentRequests(t *testing.T) {
	recovery, identity := resumeFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	recovery.Restart = func(_ context.Context, record CFOResume) error {
		if record.Harness != "claude" || record.Terminal != "cfo" || record.Session != "abc01234-5678-9012-abcd-012345678901" {
			t.Errorf("record=%+v", record)
		}
		close(entered)
		<-release
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- recovery.Run(context.Background(), identity) }()
	<-entered
	err := recovery.Run(context.Background(), identity)
	close(release)
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("second request=%v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRestartEndpointRequiresOriginTokenAndPropagatesFailure(t *testing.T) {
	for _, test := range []struct {
		name, origin, token string
		failure             bool
		want                int
	}{
		{"success", "http://board.local", "instance", false, 200},
		{"foreign origin", "http://evil.local", "instance", false, 403},
		{"missing token", "http://board.local", "", false, 403},
		{"restart failed", "http://board.local", "instance", true, 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			recovery, identity := resumeFixture(t)
			wasCalled := false
			recovery.Restart = func(context.Context, CFOResume) error {
				wasCalled = true
				if test.failure {
					return errors.New("harness could not start")
				}
				return nil
			}
			service := &Service{Instance: "instance", Options: Options{CFORecovery: recovery}, subscribers: map[chan struct{}]struct{}{}}
			handler := NewHTTP(service, "board.local", nil)
			request := httptest.NewRequest("POST", "http://board.local/api/cfo/restart", strings.NewReader(`{"identity":"`+identity+`"}`))
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-CFO-Token", test.token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || wasCalled != (test.want != 403) {
				t.Fatalf("status=%d body=%s called=%t", response.Code, response.Body.String(), wasCalled)
			}
		})
	}
}

func TestRecoveryTracksConversationChangesWithoutChangingRegistration(t *testing.T) {
	recovery, priorIdentity := resumeFixture(t)
	path := filepath.Join(recovery.State, "primary.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var primary primaryRegistration
	if err := json.Unmarshal(before, &primary); err != nil {
		t.Fatal(err)
	}
	const session = "abc01234-5678-9012-abcd-012345678909"
	if err := saveCFOConversation(recovery.State, primary, session); err != nil {
		t.Fatal(err)
	}
	if err := saveCFOConversation(recovery.State, primary, ""); err != nil {
		t.Fatal(err)
	}
	record, err := recovery.Preview()
	if err != nil || record.Session != session || record.Identity == priorIdentity {
		t.Fatalf("record=%+v error=%v", record, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("registration changed: %v", err)
	}
	primary.Process.PID++
	if err := saveCFOConversation(recovery.State, primary, session); err != nil {
		t.Fatal(err)
	}
	record, err = recovery.Preview()
	if err != nil || record.Session == session || record.Identity != priorIdentity {
		t.Fatalf("accepted another process's conversation: %+v %v", record, err)
	}
}

func TestRecoveryRefusesMissingOrCorruptConversation(t *testing.T) {
	for _, data := range []string{"", `{}`, `{"session":"--last"}`} {
		t.Run(data, func(t *testing.T) {
			recovery, _ := resumeFixture(t)
			if err := os.WriteFile(filepath.Join(recovery.State, "cfo-conversation.json"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := recovery.Preview(); err == nil {
				t.Fatal("accepted unreadable conversation")
			}
		})
	}
}
