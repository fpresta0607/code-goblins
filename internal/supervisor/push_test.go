package supervisor

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
	"github.com/fpresta0607/code-goblins/internal/webpush"
)

// The RFC 8291 example's browser keys: a test's app subscribes with them, so
// a test reads what was pushed to it.
const (
	browserPrivate = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	browserPublic  = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	browserAuth    = "BTBZMqHH6r4Tts7J_aSIgg"
	phoneEndpoint  = "https://web.push.apple.com/QGuQyavXutnMH"
)

// pushed is a push as the browser with the example's keys reads it.
func pushed(t *testing.T, body []byte) map[string]any {
	t.Helper()
	decode := func(value string) []byte { data, _ := base64.RawURLEncoding.DecodeString(value); return data }
	salt, length := body[:16], int(body[20])
	serverKey, record := body[21:21+length], body[21+length:]
	browser, err := ecdh.P256().NewPrivateKey(decode(browserPrivate))
	if err != nil {
		t.Fatal(err)
	}
	server, err := ecdh.P256().NewPublicKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := browser.ECDH(server)
	if err != nil {
		t.Fatal(err)
	}
	ikm, _ := hkdf.Key(sha256.New, shared, decode(browserAuth), "WebPush: info\x00"+string(browser.PublicKey().Bytes())+string(serverKey), 32)
	key, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, record, nil)
	if err != nil {
		t.Fatalf("the push cannot be read with the browser's keys: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimRight(bytes.TrimSuffix(bytes.TrimRight(plain, "\x00"), []byte{2}), "\x00"), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

// pushService stands in for Apple's push service: the client a test's board
// pushes with reaches it under Apple's name, and it answers each push with
// status and keeps what it was sent.
type pushService struct {
	mu     sync.Mutex
	status int
	bodies [][]byte
	client *http.Client
}

func newPushService(t *testing.T, status int) *pushService {
	t.Helper()
	p := &pushService{status: status}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.bodies = append(p.bodies, body)
		status := p.status
		p.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	p.client = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "example.com"},
	}}
	return p
}

func (p *pushService) sent() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bodies
}

// badgeBoard is a board with remote access on whose app badge pushes through
// p, with apps that turned their badge on.
func badgeBoard(t *testing.T, p *pushService, apps ...webpush.Subscription) (*Service, *remoteaccess.Manager) {
	t.Helper()
	store, h := testStore(t)
	manager := &remoteaccess.Manager{State: h.State, Target: "http://127.0.0.1:4310", Commands: &remoteCommands{}}
	if _, err := manager.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, Instance: "instance", subscribers: map[chan struct{}]struct{}{}, Options: Options{RemoteAccess: manager, Push: p.client}}
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	_, record, err := s.pushKeys()
	if err != nil {
		t.Fatal(err)
	}
	record.Apps = append(record.Apps, apps...)
	if err := s.writePush(record); err != nil {
		t.Fatal(err)
	}
	return s, manager
}

var phone = webpush.Subscription{Endpoint: phoneEndpoint, P256dh: browserPublic, Auth: browserAuth}

// keptApps is what pushFile holds of the apps.
func keptApps(t *testing.T, s *Service) []webpush.Subscription {
	t.Helper()
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	record, err := s.readPush()
	if err != nil {
		t.Fatal(err)
	}
	return record.Apps
}

// askOverlord puts a question of the CFO's on the board.
func askOverlord(t *testing.T, s *Service, id, text string) {
	t.Helper()
	if err := s.Store.acceptQuestion(Question{ID: id, Identity: strings.Repeat("c", 64), Text: text, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	s.notify()
}

// pushNewly runs one pass of the sender, failing the test on an error.
func pushNewly(t *testing.T, s *Service, seen map[string]bool) map[string]bool {
	t.Helper()
	seen, err := s.pushNew(context.Background(), seen)
	if err != nil {
		t.Fatal(err)
	}
	return seen
}

// CFO decision 1008918: the Home Screen app's badge carries the count of what
// waits on him while the app is closed, from a push with each item newly
// waiting, in the words the desktop window notifies it, and with nothing
// else. What already waited when the badge turned on is not pushed.
func TestEachNewItemIsPushedWithHowManyWait(t *testing.T) {
	// Arrange
	p := newPushService(t, http.StatusCreated)
	s, _ := badgeBoard(t, p, phone)
	askOverlord(t, s, "cfo-question-1", "Which layout should I keep?")
	seen := pushNewly(t, s, nil)

	// Act
	askOverlord(t, s, "cfo-question-2", "May I merge the release train now?")
	seen = pushNewly(t, s, seen)
	again := pushNewly(t, s, seen)

	// Assert
	sent := p.sent()
	if len(sent) != 1 || len(again) != 2 {
		t.Fatalf("pushed %d, then knew %d waiting; want one push, for the new question, and both known", len(sent), len(again))
	}
	payload := pushed(t, sent[0])
	if payload["title"] != "CFO" || payload["body"] != "The CFO asks: May I merge the release train now?" || payload["count"] != float64(2) || !strings.HasPrefix(payload["tag"].(string), "question:cfo-question-2@") {
		t.Errorf("pushed %v, want the new question in the CFO's words, its key, and a count of 2", payload)
	}
}

// One change pushes at most maxPushesAtOnce items, each with the count.
func TestOneChangePushesAtMostThreeItems(t *testing.T) {
	// Arrange
	p := newPushService(t, http.StatusCreated)
	s, _ := badgeBoard(t, p, phone)
	seen := pushNewly(t, s, nil)
	for i := range 5 {
		askOverlord(t, s, "cfo-question-"+strconv.Itoa(i+1), "Question "+strconv.Itoa(i+1)+"?")
	}

	// Act
	pushNewly(t, s, seen)

	// Assert
	if sent := p.sent(); len(sent) != maxPushesAtOnce || pushed(t, sent[0])["count"] != float64(5) {
		t.Errorf("pushed %d, want %d each counting 5", len(sent), maxPushesAtOnce)
	}
}

// Under AFK mode the CFO answers in his place and nothing is announced, so
// nothing is pushed, nor later for what arrived meanwhile; what arrives once
// it is off is.
func TestNothingIsPushedUnderAFKMode(t *testing.T) {
	// Arrange
	p := newPushService(t, http.StatusCreated)
	s, _ := badgeBoard(t, p, phone)
	seen := pushNewly(t, s, nil)
	if _, _, err := afk.TurnOn(s.Store.Home.State, "the board", nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Act
	askOverlord(t, s, "cfo-question-1", "Which layout should I keep?")
	seen = pushNewly(t, s, seen)
	duringAFK := len(p.sent())
	if _, err := afk.TurnOff(s.Store.Home.State, "the board", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	seen = pushNewly(t, s, seen)
	afterAFK := len(p.sent())
	askOverlord(t, s, "cfo-question-2", "May I merge the release train now?")
	pushNewly(t, s, seen)

	// Assert
	if duringAFK != 0 || afterAFK != 0 || len(p.sent()) != 1 {
		t.Errorf("pushed %d under AFK mode, %d once it was off, %d in all; want only the question asked after it", duringAFK, afterAFK, len(p.sent()))
	}
}

// An app its push service no longer has, as when he removed it from his Home
// Screen, is forgotten.
func TestAnAppItsPushServiceDroppedIsForgotten(t *testing.T) {
	// Arrange
	p := newPushService(t, http.StatusGone)
	s, _ := badgeBoard(t, p, phone)
	seen := pushNewly(t, s, nil)

	// Act
	askOverlord(t, s, "cfo-question-1", "Which layout should I keep?")
	pushNewly(t, s, seen)

	// Assert
	if apps := keptApps(t, s); len(apps) != 0 || len(p.sent()) != 1 {
		t.Errorf("apps = %+v after %d pushes; want the one the service dropped forgotten", apps, len(p.sent()))
	}
}

// Each app turned its badge on over the private board, so remote access off,
// as for a phone he lost, forgets every app at once, and nothing more is
// pushed to it.
func TestRemoteAccessOffForgetsEveryApp(t *testing.T) {
	// Arrange
	p := newPushService(t, http.StatusCreated)
	s, manager := badgeBoard(t, p, phone)
	seen := pushNewly(t, s, nil)
	if err := manager.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Act
	askOverlord(t, s, "cfo-question-1", "Which layout should I keep?")
	pushNewly(t, s, seen)

	// Assert
	if apps := keptApps(t, s); len(apps) != 0 || len(p.sent()) != 0 {
		t.Errorf("apps = %+v after %d pushes; want none kept and nothing pushed", apps, len(p.sent()))
	}
}

// An app the board did not keep, its push service not one it sends to, as
// from a hand edit of pushFile, is forgotten and said, never sent to.
func TestAnAppNoPushServiceKeepsIsForgottenUnsent(t *testing.T) {
	// Arrange
	p := newPushService(t, http.StatusCreated)
	s, _ := badgeBoard(t, p, phone, webpush.Subscription{Endpoint: "https://127.0.0.1:4310/api/remote", P256dh: browserPublic, Auth: browserAuth})
	seen := pushNewly(t, s, nil)

	// Act
	askOverlord(t, s, "cfo-question-1", "Which layout should I keep?")
	_, err := s.pushNew(context.Background(), seen)

	// Assert
	if apps := keptApps(t, s); err == nil || len(apps) != 1 || apps[0] != phone || len(p.sent()) != 1 {
		t.Errorf("apps = %+v, %d pushes, %v; want the stray app forgotten and said, and the phone pushed to", apps, len(p.sent()), err)
	}
}

// A board nobody turned the badge on keeps no key and asks nothing, and a
// push file that cannot be read is reported once, in the next recovery
// cycle, without waking the board again: the loop never feeds itself.
func TestTheSenderRestsWhenThereIsNothingToPush(t *testing.T) {
	for name, file := range map[string]string{"no app ever": "", "an unreadable push file": "{not json"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			p := newPushService(t, http.StatusCreated)
			store, h := testStore(t)
			s := &Service{Store: store, Instance: "instance", subscribers: map[chan struct{}]struct{}{}, Options: Options{Push: p.client}}
			if file != "" {
				if err := os.WriteFile(filepath.Join(h.State, pushFile), []byte(file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); s.keepPushing(ctx) }()
			defer func() { cancel(); <-done }()
			before := s.Revision()

			// Act
			askOverlord(t, s, "cfo-question-1", "Which layout should I keep?")
			time.Sleep(300 * time.Millisecond) // stall-accepted: the time a loop that fed itself would spin through thousands of changes.

			// Assert
			if changes := s.Revision() - before; changes > 1 {
				t.Errorf("one board change made %d", changes)
			}
			_, err := os.Stat(filepath.Join(h.State, pushFile))
			s.mu.Lock()
			reported := s.pushErr
			s.mu.Unlock()
			if file == "" && (err == nil || reported != nil) || file != "" && (reported == nil || len(p.sent()) != 0) {
				t.Errorf("%s exists: %v, reported %v, pushed %d", pushFile, err == nil, reported, len(p.sent()))
			}
		})
	}
}

// fromPhone posts body to /api/push through the private board's gate, as his
// phone does, or reads it.
func fromPhone(gate http.Handler, method, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://pc.example.ts.net:4311/api/push", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:55432"
	r.Header.Set("Origin", "https://pc.example.ts.net:4311")
	r.Header.Set("Tailscale-User-Login", "owner@example.com")
	r.Header.Set("X-CFO-Token", "instance")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	gate.ServeHTTP(w, r)
	return w
}

// postPush posts body to /api/push the way the board's own page on this PC
// does.
func postPush(board *HTTP, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://"+board.Host+"/api/push", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:50000"
	request.Header.Set("Origin", "http://"+board.Host)
	request.Header.Set("X-CFO-Token", "instance")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	board.ServeHTTP(response, request)
	return response
}

// appBody is a browser's subscription as PushSubscription.toJSON gives it.
func appBody(endpoint, key, auth string) string {
	body, _ := json.Marshal(map[string]any{"subscribe": map[string]any{"endpoint": endpoint, "keys": map[string]string{"p256dh": key, "auth": auth}}})
	return string(body)
}

// phoneBoard is a board with remote access on, as his phone reaches it
// through the gate.
func phoneBoard(t *testing.T) (*Service, *HTTP, http.Handler) {
	t.Helper()
	store, h := testStore(t)
	manager := &remoteaccess.Manager{State: h.State, Target: "http://127.0.0.1:4310", Commands: &remoteCommands{}}
	if _, err := manager.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, Instance: "instance", subscribers: map[chan struct{}]struct{}{}, Options: Options{RemoteAccess: manager, Push: http.DefaultClient}}
	board := NewHTTP(s, "127.0.0.1:4310", nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return s, board, remoteaccess.NewGate(ctx, manager, board)
}

// He turns the badge on from his Home Screen app on his phone: the board
// gives the key the app subscribes with, keeps the app's subscription once
// however often it is sent, and forgets it when he turns the badge off, from
// the phone or this PC. The board on this PC turns no app's badge on.
func TestTheAppBadgeKeepsAPhoneUntilItIsTurnedOff(t *testing.T) {
	// Arrange
	s, board, gate := phoneBoard(t)

	// Act
	key := fromPhone(gate, "GET", "")
	first := fromPhone(gate, "POST", appBody(phoneEndpoint, browserPublic, browserAuth))
	again := fromPhone(gate, "POST", appBody(phoneEndpoint, browserPublic, browserAuth))
	kept := keptApps(t, s)
	fromThisPC := postPush(board, appBody("https://fcm.googleapis.com/fcm/send/abc", browserPublic, browserAuth))
	off := postPush(board, `{"unsubscribe":"`+phoneEndpoint+`"}`)

	// Assert
	var answer struct{ Key string }
	if key.Code != 200 || json.Unmarshal(key.Body.Bytes(), &answer) != nil || len(answer.Key) != 87 {
		t.Fatalf("GET /api/push = %d %s, want the board's public key", key.Code, key.Body)
	}
	if first.Code != 200 || again.Code != 200 || len(kept) != 1 || kept[0] != phone {
		t.Fatalf("turning it on = %d, %d; kept %+v; want the phone kept once", first.Code, again.Code, kept)
	}
	if fromThisPC.Code != http.StatusForbidden || off.Code != 200 || len(keptApps(t, s)) != 0 {
		t.Errorf("from this PC = %d, off = %d, apps %+v; want this PC refused and the phone forgotten", fromThisPC.Code, off.Code, keptApps(t, s))
	}
}

// The board keeps the newest maxPushApps apps: a new one replaces the oldest.
func TestTheAppBadgeKeepsTheNewestApps(t *testing.T) {
	// Arrange
	s, _, gate := phoneBoard(t)

	// Act
	for i := range maxPushApps + 1 {
		if response := fromPhone(gate, "POST", appBody(phoneEndpoint+strconv.Itoa(i), browserPublic, browserAuth)); response.Code != 200 {
			t.Fatalf("app %d = %d %s", i, response.Code, response.Body)
		}
	}

	// Assert
	if apps := keptApps(t, s); len(apps) != maxPushApps || apps[0].Endpoint != phoneEndpoint+"1" || apps[maxPushApps-1].Endpoint != phoneEndpoint+strconv.Itoa(maxPushApps) {
		t.Errorf("kept %d apps, from %+v; want the newest %d", len(apps), apps, maxPushApps)
	}
}

// Only a subscription a browser made through a push service browsers use is
// kept, and a board without the app badge keeps none.
func TestTheAppBadgeRefusesWhatNoBrowserSubscribedWith(t *testing.T) {
	notAPoint := base64.RawURLEncoding.EncodeToString(append([]byte{4}, bytes.Repeat([]byte{1}, 64)...))
	for name, c := range map[string]struct {
		body        string
		isUnpushing bool
		want        int
	}{
		"a server that is no push service":     {appBody("https://example.com/collect", browserPublic, browserAuth), false, http.StatusBadRequest},
		"a push service over plain HTTP":       {appBody("http://web.push.apple.com/QGuQyavXutnMH", browserPublic, browserAuth), false, http.StatusBadRequest},
		"this PC's own board":                  {appBody("https://127.0.0.1:4310/api/remote", browserPublic, browserAuth), false, http.StatusBadRequest},
		"a key that is not on the curve":       {appBody(phoneEndpoint, notAPoint, browserAuth), false, http.StatusBadRequest},
		"a short secret":                       {appBody(phoneEndpoint, browserPublic, "c2hvcnQ"), false, http.StatusBadRequest},
		"turning on and off at once":           {`{"subscribe":{"endpoint":"` + phoneEndpoint + `"},"unsubscribe":"` + phoneEndpoint + `"}`, false, http.StatusBadRequest},
		"neither":                              {`{}`, false, http.StatusBadRequest},
		"a board that pushes to no app at all": {appBody(phoneEndpoint, browserPublic, browserAuth), true, http.StatusConflict},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, _, gate := phoneBoard(t)
			if c.isUnpushing {
				s.Options.Push = nil
			}

			// Act
			response := fromPhone(gate, "POST", c.body)

			// Assert
			if response.Code != c.want {
				t.Fatalf("POST /api/push = %d %s, want %d", response.Code, response.Body, c.want)
			}
			if data, err := os.ReadFile(filepath.Join(s.Store.Home.State, pushFile)); err == nil {
				var record pushRecord
				if json.Unmarshal(data, &record) != nil || len(record.Apps) != 0 {
					t.Errorf("apps = %+v, want none kept", record.Apps)
				}
			}
		})
	}
}

// A goblin's question the CFO has left counts on the badge, as the page
// counts it, but is never pushed, as the page never alerts it.
func TestAGoblinsQuestionTheCFOLeftCountsButIsNotPushed(t *testing.T) {
	// Arrange
	p := newPushService(t, http.StatusCreated)
	s, _ := badgeBoard(t, p, phone)
	seen := pushNewly(t, s, nil)

	// Act
	if err := s.Store.acceptQuestion(Question{ID: "notify-olive-1", Identity: strings.Repeat("d", 64), Task: "olive-task", Generation: "1", Seq: 1, Text: "Which port?", CreatedAt: time.Now().UTC().Add(-11 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	s.notify()
	seen = pushNewly(t, s, seen)
	quiet := len(p.sent())
	askOverlord(t, s, "cfo-question-1", "Which layout should I keep?")
	pushNewly(t, s, seen)

	// Assert
	if sent := p.sent(); quiet != 0 || len(sent) != 1 || pushed(t, sent[0])["count"] != float64(2) {
		t.Errorf("pushed %d for the goblin's question, %d in all; want none for it, then the CFO's question counting 2", quiet, len(sent))
	}
}
