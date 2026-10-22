package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// stepCard is a goblin's card for a step only the Overlord can take, on a
// board of its own.
func stepCard(t *testing.T, change func(*CredentialRequest)) *credentialBoard {
	t.Helper()
	filed := CredentialRequest{ID: "cred-0123456789abcdef", Identity: strings.Repeat("c", 64), By: "cfo", Task: "task-1", Project: "throwaway", Why: "Create the shop's test account", Link: "https://example.com/signup", Steps: []string{"Create the account", "Accept the terms"}}
	if change != nil {
		change(&filed)
	}
	return newCredentialBoardFor(t, filed)
}

// closeBody is a Done or Dismiss for the board's card.
func (b *credentialBoard) closeBody(action string) string {
	b.t.Helper()
	data, err := json.Marshal(struct {
		ID         string `json:"id"`
		Generation string `json:"generation"`
		Action     string `json:"action"`
	}{b.request.ID, b.request.Generation, action})
	if err != nil {
		b.t.Fatal(err)
	}
	return string(data)
}

// A plain value goes back to the goblin and a secret into the vault: the
// card closes once both are in, the vault never holds the plain value, and
// the board's snapshot carries neither.
func TestStepCardSendsAPlainValueToTheGoblinAndASecretToTheVault(t *testing.T) {
	// Arrange
	b := stepCard(t, func(r *CredentialRequest) {
		r.Names, r.Plain = []string{"STRIPE_SECRET_KEY"}, []string{"ACCOUNT_EMAIL"}
		r.Labels = map[string]string{"ACCOUNT_EMAIL": "The account's email", "STRIPE_SECRET_KEY": "Its secret key"}
	})
	canary := newCanary(t)
	email := "shop-owner@example.com"

	// Act
	plain := b.post(b.handler, b.body(map[string]string{"ACCOUNT_EMAIL": email}), nil)
	afterPlain := b.stored()
	secret := b.post(b.handler, b.body(map[string]string{"STRIPE_SECRET_KEY": canary}), nil)
	b.service.credentialWork.Wait()
	snapshot, err := b.service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	published, _ := json.Marshal(snapshot)

	// Assert
	if plain.Code != http.StatusOK || secret.Code != http.StatusOK {
		t.Fatalf("saves = %d %s, %d %s", plain.Code, plain.Body, secret.Code, secret.Body)
	}
	if afterPlain.State != "open" || afterPlain.Values["ACCOUNT_EMAIL"] != email || !slices.Equal(afterPlain.Received, []string{"ACCOUNT_EMAIL"}) {
		t.Fatalf("after the plain value = %+v, want it open holding the value for the goblin", afterPlain)
	}
	if _, found := b.vaultValue("ACCOUNT_EMAIL"); found {
		t.Fatal("the plain value went into the vault")
	}
	if got, found := b.vaultValue("STRIPE_SECRET_KEY"); !found || got != canary {
		t.Fatal("the secret did not reach the vault")
	}
	if request := b.stored(); request.State != "saved" || request.ClosedAt == nil {
		t.Fatalf("request = %+v, want it closed once both are in", request)
	}
	if strings.Contains(string(published), email) || strings.Contains(string(published), canary) {
		t.Fatal("the board's snapshot carries a value")
	}
	if !strings.Contains(string(published), "The account's email") {
		t.Fatal("the snapshot does not carry the input's label")
	}
}

// A plain field goes to the goblin's conversation, so a key pasted into it
// is refused, kept nowhere and never repeated.
func TestStepCardRefusesAKeyInAPlainField(t *testing.T) {
	// Arrange
	b := stepCard(t, func(r *CredentialRequest) { r.Plain = []string{"ACCOUNT_EMAIL"} })
	canary := newCanary(t)

	// Act
	response := b.post(b.handler, b.body(map[string]string{"ACCOUNT_EMAIL": "my key is " + canary}), nil)

	// Assert
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "goblin") {
		t.Fatalf("save = %d %s, want it refused saying the field goes to the goblin", response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), canary) {
		t.Fatal("the refusal repeats the key")
	}
	if request := b.stored(); request.State != "open" || len(request.Values) != 0 || len(request.Received) != 0 {
		t.Fatalf("request = %+v, want it open with nothing received", request)
	}
	if found := filesHolding(t, b.home.Root, canary); len(found) != 0 {
		t.Fatalf("the key is in %v", found)
	}
}

// A plain field whose name says it is an ID takes a short identifier, such as
// an OAuth client ID, without a warning, while a long token shape, a key's
// prefix or a field not named as an ID still reads as a key, and the refusal
// says so, so the card can offer him to say the value is no secret.
func TestStepCardSendsAnIdentifierInAnIDFieldAndWarnsOfAKeyElsewhere(t *testing.T) {
	for name, c := range map[string]struct {
		field, value string
		isRefused    bool
	}{
		"a mixed case client ID in an ID field":    {"MADE_UP_CLIENT_ID", "k7Qx9L" + "m2Vb4Rt8Nc", false},
		"twenty letters and digits in an ID field": {"MADE_UP_CLIENT_ID", "A1b2C3d4E5" + "f6G7h8I9j0", false},
		"twenty one characters in an ID field":     {"MADE_UP_CLIENT_ID", "A1b2C3d4E5" + "f6G7h8I9j0K", true},
		"a key's prefix in an ID field":            {"MADE_UP_CLIENT_ID", "ghp" + "_Qx9Lm2Vb4" + "Rt8Nc6Y", true},
		"a short identifier in a field not an ID":  {"MADE_UP_ACCOUNT_REF", "k7Qx9L" + "m2Vb4Rt8Nc", true},
		"a long token in an ID field":              {"MADE_UP_CLIENT_ID", "Zk3Qx9Lm2Vb7" + "Nc4Rt8Wy1Hs5" + "Jd6Fg0A", true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			b := stepCard(t, func(r *CredentialRequest) { r.Plain = []string{c.field} })

			// Act
			response := b.post(b.handler, b.body(map[string]string{c.field: c.value}), nil)

			// Assert
			if c.isRefused {
				if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"looks_like_key":true`) {
					t.Fatalf("save = %d %s, want it refused as a key", response.Code, response.Body)
				}
				if strings.Contains(response.Body.String(), c.value) {
					t.Fatal("the refusal repeats the value")
				}
				if request := b.stored(); len(request.Values) != 0 {
					t.Fatalf("request = %+v, want nothing received", request)
				}
				return
			}
			if response.Code != http.StatusOK {
				t.Fatalf("save = %d %s, want the identifier sent", response.Code, response.Body)
			}
			if request := b.stored(); request.Values[c.field] != c.value || !slices.Contains(request.Received, c.field) {
				t.Fatalf("request = %+v, want %s received", request, c.field)
			}
		})
	}
}

// He can say a value is no secret, which sends it to the goblin though it is
// shaped like a key, for that field alone.
func TestStepCardSendsAKeyShapedPlainValueHeSaysIsNoSecret(t *testing.T) {
	// Arrange
	b := stepCard(t, func(r *CredentialRequest) { r.Plain = []string{"MADE_UP_ACCOUNT_REF", "MADE_UP_OTHER_REF"} })
	value := "Zk3Qx9Lm2Vb7" + "Nc4Rt8Wy1Hs5" + "Jd6Fg0A"
	notSecret := func(names ...string) string {
		data, err := json.Marshal(struct {
			ID         string            `json:"id"`
			Generation string            `json:"generation"`
			Values     map[string]string `json:"values"`
			NotSecret  []string          `json:"not_secret"`
		}{b.request.ID, b.request.Generation, map[string]string{"MADE_UP_ACCOUNT_REF": value}, names})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	// Act
	refused := b.post(b.handler, notSecret(), nil)
	forAnother := b.post(b.handler, notSecret("MADE_UP_OTHER_REF"), nil)
	sent := b.post(b.handler, notSecret("MADE_UP_ACCOUNT_REF"), nil)

	// Assert
	if refused.Code != http.StatusBadRequest || forAnother.Code != http.StatusBadRequest {
		t.Fatalf("without his word for the field: %d and %d, want both refused", refused.Code, forAnother.Code)
	}
	if sent.Code != http.StatusOK {
		t.Fatalf("with his word: %d %s, want it sent", sent.Code, sent.Body)
	}
	if request := b.stored(); request.Values["MADE_UP_ACCOUNT_REF"] != value || !slices.Contains(request.Received, "MADE_UP_ACCOUNT_REF") {
		t.Fatalf("request = %+v, want the value received", request)
	}
}

// A card of clicks alone closes on his Done, and tells the CFO. A card that
// asks for a value closes when the value arrives, not on a Done.
func TestStepCardWithNoInputsClosesOnDone(t *testing.T) {
	t.Run("a card of clicks", func(t *testing.T) {
		// Arrange
		b := stepCard(t, nil)

		// Act
		response := b.send(b.handler, "/api/credentials/close", b.closeBody("done"), nil)
		b.service.credentialWork.Wait()

		// Assert
		if response.Code != http.StatusOK {
			t.Fatalf("done = %d %s", response.Code, response.Body)
		}
		if request := b.stored(); request.State != "done" || request.ClosedAt == nil {
			t.Fatalf("request = %+v, want it done", request)
		}
		pending, err := wake.Pending(b.home.State)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 1 || !strings.Contains(pending[0].Detail, "pressed Done") {
			t.Fatalf("wakes = %+v, want one saying he pressed Done", pending)
		}
	})
	t.Run("a card that asks for a value", func(t *testing.T) {
		// Arrange
		b := stepCard(t, func(r *CredentialRequest) { r.Plain = []string{"ACCOUNT_EMAIL"} })

		// Act
		response := b.send(b.handler, "/api/credentials/close", b.closeBody("done"), nil)

		// Assert
		if response.Code != http.StatusConflict {
			t.Fatalf("done = %d %s, want it refused", response.Code, response.Body)
		}
		if request := b.stored(); request.State != "open" {
			t.Fatalf("request = %+v, want it open", request)
		}
	})
}

// Dismiss takes any open card off the Command Center, from any board, and
// the CFO hears the goblin goes on without it. A dismissed card takes no
// value.
func TestDismissTakesAnyCardBack(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*CredentialRequest)
	}{
		{"a credential card", func(r *CredentialRequest) { r.Names, r.Steps, r.Link = []string{"STRIPE_SECRET_KEY"}, nil, "" }},
		{"a step card", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := stepCard(t, test.change)

			// Act
			response := b.send(b.handler, "/api/credentials/close", b.closeBody("dismiss"), func(r *http.Request) { r.RemoteAddr = "100.64.0.7:50000" })
			b.service.credentialWork.Wait()
			late := b.post(b.handler, b.body(map[string]string{"STRIPE_SECRET_KEY": newCanary(t)}), nil)

			// Assert
			if response.Code != http.StatusOK {
				t.Fatalf("dismiss = %d %s", response.Code, response.Body)
			}
			if request := b.stored(); request.State != "dismissed" || request.ClosedAt == nil {
				t.Fatalf("request = %+v, want it dismissed", request)
			}
			if late.Code != http.StatusConflict {
				t.Fatalf("a save after the dismiss = %d, want it refused", late.Code)
			}
			pending, err := wake.Pending(b.home.State)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 || !strings.Contains(pending[0].Detail, "dismissed") || !strings.Contains(pending[0].Detail, "without it") {
				t.Fatalf("wakes = %+v, want one saying the goblin goes on without it", pending)
			}
		})
	}
}

// Other on a card is his free answer in place of the step: the card closes
// with his words for the goblin's wait to read, the CFO hears them, and words
// shaped like a key are refused without being kept or repeated.
func TestOtherOnACardClosesItWithHisWord(t *testing.T) {
	answerBody := func(b *credentialBoard, text string) string {
		data, _ := json.Marshal(map[string]string{"id": b.request.ID, "generation": b.request.Generation, "action": "answer", "text": text})
		return string(data)
	}
	t.Run("his word", func(t *testing.T) {
		// Arrange
		b := stepCard(t, func(r *CredentialRequest) { r.Names = []string{"FLEET_GITHUB_TOKEN"} })
		word := "Use the fleet bot's token, it is already in the vault"

		// Act
		response := b.send(b.handler, "/api/credentials/close", answerBody(b, word), nil)
		b.service.credentialWork.Wait()

		// Assert
		if response.Code != http.StatusOK {
			t.Fatalf("answer = %d %s", response.Code, response.Body)
		}
		if request := b.stored(); request.State != "answered" || request.Reason != word || request.ClosedAt == nil {
			t.Fatalf("request = %+v, want it answered with his word", request)
		}
		pending, err := wake.Pending(b.home.State)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 1 || !strings.Contains(pending[0].Detail, word) {
			t.Fatalf("wakes = %+v, want the CFO told his word", pending)
		}
	})
	t.Run("a key in his word", func(t *testing.T) {
		// Arrange
		b := stepCard(t, nil)
		canary := newCanary(t)

		// Act
		response := b.send(b.handler, "/api/credentials/close", answerBody(b, "here it is "+canary), nil)
		empty := b.send(b.handler, "/api/credentials/close", answerBody(b, " "), nil)

		// Assert
		if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), canary) || empty.Code != http.StatusBadRequest {
			t.Fatalf("answers = %d %s, %d; want both refused without the key", response.Code, response.Body, empty.Code)
		}
		if request := b.stored(); request.State != "open" {
			t.Fatalf("request = %+v, want it open", request)
		}
		if found := filesHolding(t, b.home.Root, canary); len(found) != 0 {
			t.Fatalf("the key is in %v", found)
		}
	})
}

func TestThePencilBringsBackACardHeClosed(t *testing.T) {
	reopenBody := func(b *credentialBoard, generation string) string {
		data, _ := json.Marshal(map[string]string{"id": b.request.ID, "generation": generation})
		return string(data)
	}
	for _, test := range []struct {
		name  string
		close string
	}{
		{"done", `{"action":"done"}`},
		{"dismissed", `{"action":"dismiss"}`},
		{"answered", `{"action":"answer","text":"The account already existed"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := stepCard(t, nil)
			var press map[string]string
			if err := json.Unmarshal([]byte(test.close), &press); err != nil {
				t.Fatal(err)
			}
			press["id"], press["generation"] = b.request.ID, b.request.Generation
			closing, _ := json.Marshal(press)
			if response := b.send(b.handler, "/api/credentials/close", string(closing), nil); response.Code != http.StatusOK {
				t.Fatalf("close = %d %s", response.Code, response.Body)
			}
			b.service.credentialWork.Wait()
			closed := b.stored()

			// Act
			response := b.send(b.handler, "/api/credentials/reopen", reopenBody(b, closed.Generation), func(r *http.Request) { r.RemoteAddr = "100.64.0.7:50000" })
			b.service.credentialWork.Wait()

			// Assert
			if response.Code != http.StatusOK {
				t.Fatalf("reopen = %d %s", response.Code, response.Body)
			}
			reopened := b.stored()
			if reopened.State != "open" || reopened.ClosedAt != nil || reopened.Reason != "" || reopened.Generation == closed.Generation || reopened.Generation == "" {
				t.Fatalf("reopened = %+v, want it open again under a new generation", reopened)
			}
			pending, err := wake.Pending(b.home.State)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) == 0 {
				t.Fatal("the CFO was not told the card is back")
			}
			if last := pending[len(pending)-1].Detail; !strings.Contains(last, "brought back") || !strings.Contains(last, b.request.ID) {
				t.Fatalf("last wake = %q, want the CFO told the card is back", last)
			}
			if again := b.send(b.handler, "/api/credentials/reopen", reopenBody(b, reopened.Generation), nil); again.Code != http.StatusConflict {
				t.Fatalf("a second reopen of an open card = %d %s, want it refused", again.Code, again.Body)
			}
			if stale := b.send(b.handler, "/api/credentials/close", b.closeBody("done"), nil); stale.Code != http.StatusConflict {
				t.Fatalf("an old card closed the reopened card: %d %s", stale.Code, stale.Body)
			}
			b.request = reopened
			if done := b.send(b.handler, "/api/credentials/close", b.closeBody("done"), nil); done.Code != http.StatusOK {
				t.Fatalf("the reopened card could not be completed: %d %s", done.Code, done.Body)
			}
		})
	}
	for _, test := range []struct {
		name       string
		state      string
		isExpired  bool
		generation func(CredentialRequest) string
	}{
		{"expired", "dismissed", true, func(r CredentialRequest) string { return r.Generation }},
		{"saved", "saved", false, func(r CredentialRequest) string { return r.Generation }},
		{"withdrawn", "withdrawn", false, func(r CredentialRequest) string { return r.Generation }},
		{"another generation", "dismissed", false, func(CredentialRequest) string { return strings.Repeat("f", 32) }},
	} {
		t.Run("refuses a card "+test.name, func(t *testing.T) {
			// Arrange
			b := stepCard(t, nil)
			if _, err := b.service.closeCredential(b.request.ID, "", test.state, "cfo", "Closed for the test"); err != nil {
				t.Fatal(err)
			}
			b.service.credentialWork.Wait()
			if test.isExpired {
				if _, err := b.service.Store.updateCredential(b.request.ID, func(r *CredentialRequest) { r.ExpiresAt = time.Now().Add(-time.Minute) }); err != nil {
					t.Fatal(err)
				}
			}
			closed := b.stored()

			// Act
			response := b.send(b.handler, "/api/credentials/reopen", reopenBody(b, test.generation(closed)), nil)

			// Assert
			if response.Code != http.StatusConflict {
				t.Fatalf("reopen = %d %s, want it refused", response.Code, response.Body)
			}
			if request := b.stored(); request.State != test.state {
				t.Fatalf("request = %+v, want it still %s", request, test.state)
			}
		})
	}
}

func TestHistoryReopenRequiresTheCardGeneration(t *testing.T) {
	for _, body := range []string{`{`, `{}`, `{"id":"cred-0123456789abcdef"}`, `{"generation":"old-generation"}`} {
		t.Run(body, func(t *testing.T) {
			// Arrange
			b := stepCard(t, nil)

			// Act
			response := b.send(b.handler, "/api/credentials/reopen", body, nil)

			// Assert
			if response.Code != http.StatusBadRequest {
				t.Fatalf("reopen = %d %s, want a bad request", response.Code, response.Body)
			}
			if request := b.stored(); request.State != "open" || request.Generation != b.request.Generation {
				t.Fatal("a malformed request changed the card")
			}
		})
	}
}

// The window button asks the goblin's own browser session to show itself,
// only from the board on this PC, and only on a card that offers it.
func TestStepCardAsksForTheGoblinsWindowOnlyFromThisPC(t *testing.T) {
	windowBody := func(b *credentialBoard) string {
		data, _ := json.Marshal(struct {
			ID         string `json:"id"`
			Generation string `json:"generation"`
		}{b.request.ID, b.request.Generation})
		return string(data)
	}
	t.Run("from this PC", func(t *testing.T) {
		b := stepCard(t, func(r *CredentialRequest) { r.Window = true })
		response := b.send(b.handler, "/api/credentials/window", windowBody(b), nil)
		if response.Code != http.StatusOK || b.stored().WindowState != "requested" {
			t.Fatalf("window = %d %s, state %q; want it requested", response.Code, response.Body, b.stored().WindowState)
		}
	})
	t.Run("from another machine", func(t *testing.T) {
		b := stepCard(t, func(r *CredentialRequest) { r.Window = true })
		response := b.send(b.handler, "/api/credentials/window", windowBody(b), func(r *http.Request) { r.RemoteAddr = "100.64.0.7:50000" })
		if response.Code != http.StatusForbidden || b.stored().WindowState != "" {
			t.Fatalf("window = %d, state %q; want it refused", response.Code, b.stored().WindowState)
		}
	})
	t.Run("on a card without a window", func(t *testing.T) {
		b := stepCard(t, nil)
		response := b.send(b.handler, "/api/credentials/window", windowBody(b), nil)
		if response.Code != http.StatusConflict || b.stored().WindowState != "" {
			t.Fatalf("window = %d, state %q; want it refused", response.Code, b.stored().WindowState)
		}
	})
}

// A step card takes labels, plain inputs and a window only in their shape:
// a label of a few words for an input it asks for, plain names that are
// variable names asked once, and a window only for a goblin's page.
func TestStepCardTakesOnlyWellFormedInputs(t *testing.T) {
	random := "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
	base := CredentialRequest{ID: "cred-0123456789abcdef", Identity: strings.Repeat("c", 64), By: "cfo", Task: "task-1", Project: "throwaway", Why: "Create the shop's test account", Link: "https://example.com/signup"}
	for _, test := range []struct {
		name    string
		change  func(*CredentialRequest)
		isTaken bool
	}{
		{"a link and no inputs", func(r *CredentialRequest) {}, true},
		{"steps and no link", func(r *CredentialRequest) { r.Link, r.Steps = "", []string{"Accept the terms"} }, true},
		{"a plain input with a label", func(r *CredentialRequest) {
			r.Plain, r.Labels = []string{"ACCOUNT_EMAIL"}, map[string]string{"ACCOUNT_EMAIL": "The account's email"}
		}, true},
		{"a window on a goblin's page", func(r *CredentialRequest) { r.Window = true }, true},
		{"no link, step or input", func(r *CredentialRequest) { r.Link = "" }, false},
		{"a plain name that is no variable", func(r *CredentialRequest) { r.Plain = []string{"account email"} }, false},
		{"a name asked plain and secret", func(r *CredentialRequest) { r.Names, r.Plain = []string{"ACCOUNT_ID"}, []string{"ACCOUNT_ID"} }, false},
		{"eleven inputs", func(r *CredentialRequest) {
			for i := range 6 {
				r.Names = append(r.Names, "SECRET_"+string(rune('A'+i)))
				r.Plain = append(r.Plain, "PLAIN_"+string(rune('A'+i)))
			}
		}, false},
		{"a label for no input", func(r *CredentialRequest) { r.Labels = map[string]string{"ACCOUNT_EMAIL": "The account's email"} }, false},
		{"a label over 60 characters", func(r *CredentialRequest) {
			r.Plain, r.Labels = []string{"ACCOUNT_EMAIL"}, map[string]string{"ACCOUNT_EMAIL": strings.Repeat("email ", 11)}
		}, false},
		{"a label holding a key", func(r *CredentialRequest) {
			r.Plain, r.Labels = []string{"ACCOUNT_EMAIL"}, map[string]string{"ACCOUNT_EMAIL": "use " + random}
		}, false},
		{"a window with no link", func(r *CredentialRequest) { r.Window, r.Link, r.Steps = true, "", []string{"Sign in"} }, false},
		{"a window for no goblin", func(r *CredentialRequest) { r.Window, r.Task = true, "" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			t.Setenv(auth.StoreDirEnv, t.TempDir())
			s := &Service{Store: store, Options: Options{Credentials: auth.OpenStore}}
			request := base
			test.change(&request)

			// Act
			_, err := s.acceptCredentialRequest(request)

			// Assert
			if (err == nil) != test.isTaken {
				t.Fatalf("accept = %v, want taken %v", err, test.isTaken)
			}
			if err != nil && strings.Contains(err.Error(), random) {
				t.Fatal("the refusal repeats the value")
			}
		})
	}
}

// Two open step cards of one goblin that ask for nothing are two cards, not
// a refused duplicate, while the same names asked twice still are one.
func TestTwoStepCardsOfOneGoblinAreTwoCards(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	t.Setenv(auth.StoreDirEnv, t.TempDir())
	s := &Service{Store: store, Options: Options{Credentials: auth.OpenStore}}
	first := CredentialRequest{ID: "cred-0123456789abcdef", Identity: strings.Repeat("c", 64), By: "cfo", Project: "throwaway", Why: "Create the shop's test account", Link: "https://example.com/signup"}
	second := first
	second.ID, second.Why = "cred-1111111111111111", "Confirm the account's email"

	// Act
	_, firstErr := s.acceptCredentialRequest(first)
	_, secondErr := s.acceptCredentialRequest(second)

	// Assert
	if firstErr != nil || secondErr != nil || len(store.Snapshot().Credentials) != 2 {
		t.Fatalf("accept = %v, %v; want both cards on the board", firstErr, secondErr)
	}
}

// A goblin's open card leaves the Command Center once its goblin is gone or
// was replaced, or reported done or failed after asking: nobody waits on it.
func TestAGoblinsOpenCardIsWithdrawnOnceNobodyWaitsOnIt(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(t *testing.T, stateDir string, meta state.TaskMeta)
		want   string
	}{
		{"its goblin is gone", func(t *testing.T, stateDir string, meta state.TaskMeta) {
			if err := state.RemoveTaskMeta(stateDir, meta.ID); err != nil {
				t.Fatal(err)
			}
		}, "withdrawn"},
		{"its goblin was replaced", func(t *testing.T, stateDir string, meta state.TaskMeta) {
			meta.SpawnGen = "g2"
			if err := state.WriteTaskMeta(stateDir, meta); err != nil {
				t.Fatal(err)
			}
		}, "withdrawn"},
		{"its goblin reported done", func(t *testing.T, stateDir string, meta state.TaskMeta) {
			if err := state.AppendStatus(stateDir, meta.ID, "done: shipped"); err != nil {
				t.Fatal(err)
			}
		}, "withdrawn"},
		{"its goblin is still working", func(t *testing.T, stateDir string, meta state.TaskMeta) {
			if err := state.AppendStatus(stateDir, meta.ID, "working: waiting on the card"); err != nil {
				t.Fatal(err)
			}
		}, "open"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			t.Setenv(auth.StoreDirEnv, t.TempDir())
			s := &Service{Store: store, Options: Options{Credentials: auth.OpenStore}}
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.acceptCredentialRequest(CredentialRequest{ID: "cred-0123456789abcdef", Identity: goblinIdentity(meta), By: "goblin", Task: meta.ID, Project: "throwaway", Why: "Create the shop's test account", Link: "https://example.com/signup"}); err != nil {
				t.Fatal(err)
			}
			// A report is stamped to the second, so the card was asked a
			// minute before it.
			if _, err := store.updateCredential("cred-0123456789abcdef", func(r *CredentialRequest) { r.CreatedAt = r.CreatedAt.Add(-time.Minute) }); err != nil {
				t.Fatal(err)
			}
			test.change(t, h.State, meta)

			// Act
			retireErr := store.retireGoblinCredentials()

			// Assert
			if retireErr != nil {
				t.Fatal(retireErr)
			}
			if got := store.Snapshot().Credentials[0]; got.State != test.want {
				t.Fatalf("card = %+v, want %s", got, test.want)
			}
		})
	}
}

// A goblin waiting on its card continues once the board records his Done,
// and reads the plain values he typed straight from its card. The look
// whatever the record's stamp says is held off, so only the stamp the press
// gives the record can end the wait, and no bound on how soon a busy PC gets
// there is needed to tell the two apart.
func TestWaitingOnACardEndsWhenTheBoardRecordsItClosed(t *testing.T) {
	defer func(recheck int) { credentialWaitRecheck = recheck }(credentialWaitRecheck)
	credentialWaitRecheck = math.MaxInt
	for _, test := range []struct {
		name   string
		change func(*CredentialRequest)
		act    func(*credentialBoard) int
		state  string
	}{
		{"Done on a card of clicks", nil, func(b *credentialBoard) int {
			return b.send(b.handler, "/api/credentials/close", b.closeBody("done"), nil).Code
		}, "done"},
		{"a value arriving", func(r *CredentialRequest) { r.Plain = []string{"ACCOUNT_EMAIL"} }, func(b *credentialBoard) int {
			return b.post(b.handler, b.body(map[string]string{"ACCOUNT_EMAIL": "shop-owner@example.com"}), nil).Code
		}, "saved"},
		{"Dismiss", nil, func(b *credentialBoard) int {
			return b.send(b.handler, "/api/credentials/close", b.closeBody("dismiss"), nil).Code
		}, "dismissed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			b := stepCard(t, test.change)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			type outcome struct {
				request CredentialRequest
				err     error
			}
			looked := make(chan struct{})
			ended := make(chan outcome, 1)
			go func() {
				request, err := WaitCredential(ctx, b.home.State, b.request.ID, func(CredentialRequest) { close(looked) })
				ended <- outcome{request, err}
			}()
			select {
			case <-looked:
			case got := <-ended:
				t.Fatalf("the wait ended before the press: %+v %v", got.request, got.err)
			}

			// Act
			code := test.act(b)
			got := <-ended

			// Assert
			if code != http.StatusOK || got.err != nil {
				t.Fatalf("press = %d, wait = %v", code, got.err)
			}
			if got.request.State != test.state {
				t.Fatalf("the wait ended on %+v, want %s", got.request, test.state)
			}
			if test.state == "saved" && got.request.Values["ACCOUNT_EMAIL"] != "shop-owner@example.com" {
				t.Fatalf("values = %v, want the email he typed", got.request.Values)
			}
		})
	}
}

// A wait for a card the board refused, which is neither on the board nor in
// its inbox, ends saying so rather than waiting a day.
func TestWaitingOnACardTheBoardNeverTookEnds(t *testing.T) {
	// Arrange
	_, h := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	_, err := WaitCredential(ctx, h.State, "cred-0123456789abcdef", nil)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "not on the board") {
		t.Fatalf("wait = %v, want it to end saying the card is not on the board", err)
	}
	if ctx.Err() != nil {
		t.Fatal("the wait ran out its time instead of ending")
	}
}

// Done on a goblin's step card, by the Overlord or the CFO, or its inputs
// arriving, also closes its wait on the Overlord that was opened while the
// card was open, which asked him for the same step, saying who closed it. A wait opened before the card, a wait on a page and
// another goblin's wait are cards of their own and stay open, and a card the
// CFO filed for the goblin closes none of its waits.
func TestDoneOnAStepCardClosesTheWaitOpenedForIt(t *testing.T) {
	sent := func(response *httptest.ResponseRecorder) error {
		if response.Code != http.StatusOK {
			return fmt.Errorf("%d %s", response.Code, response.Body)
		}
		return nil
	}
	done := func(b *credentialBoard) error {
		return sent(b.send(b.handler, "/api/credentials/close", b.closeBody("done"), nil))
	}
	byGoblin := func(r *CredentialRequest) { r.By = "goblin" }
	for _, tc := range []struct {
		name   string
		change func(*CredentialRequest)
		close  func(*credentialBoard) error
		want   string
		reason string
	}{
		{"done by the Overlord", byGoblin, done, "answered", "You pressed Done"},
		{"done by the CFO", byGoblin, func(b *credentialBoard) error {
			_, err := b.service.closeCredential(b.request.ID, "", "done", "cfo", "")
			return err
		}, "answered", "The CFO closed"},
		{"saved", func(r *CredentialRequest) { r.By, r.Plain = "goblin", []string{"ACCOUNT_EMAIL"} }, func(b *credentialBoard) error {
			return sent(b.post(b.handler, b.body(map[string]string{"ACCOUNT_EMAIL": "shop-owner@example.com"}), nil))
		}, "answered", "its inputs arrived"},
		{"answered by the Overlord", byGoblin, func(b *credentialBoard) error {
			data, err := json.Marshal(map[string]string{"id": b.request.ID, "generation": b.request.Generation, "action": "answer", "text": "which plan?"})
			if err != nil {
				return err
			}
			return sent(b.send(b.handler, "/api/credentials/close", string(data), nil))
		}, "answered", "You wrote on its step card"},
		{"filed by the CFO", nil, done, "open", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			b := stepCard(t, tc.change)
			wait := func(id, task string, opened time.Duration, page string) Review {
				r := openReview(id, task)
				r.CreatedAt = b.request.CreatedAt.Add(opened)
				r.UpdatedAt = r.CreatedAt
				if page != "" {
					r.Lavish, r.LavishPage = "http://127.0.0.1:4387/session/f26e", page
				}
				return r
			}
			for _, r := range []Review{
				wait("waiting-task-1-7", "task-1", time.Second, ""),
				wait("waiting-task-1-3", "task-1", -time.Minute, ""),
				wait("waiting-task-1-8", "task-1", time.Second, `C:\work\.lavish\plan.html`),
				wait("waiting-task-2-9", "task-2", time.Second, ""),
			} {
				if err := b.service.Store.acceptReview(r); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			err := tc.close(b)
			b.service.credentialWork.Wait()

			// Assert
			if err != nil || b.stored().State == "open" {
				t.Fatalf("close = %v, card %s", err, b.stored().State)
			}
			want := map[string]string{"waiting-task-1-7": tc.want, "waiting-task-1-3": "open", "waiting-task-1-8": "open", "waiting-task-2-9": "open"}
			for _, r := range b.service.Store.Snapshot().Reviews {
				if r.State != want[r.ID] {
					t.Errorf("wait %s = %s, want %s", r.ID, r.State, want[r.ID])
				}
				if r.ID == "waiting-task-1-7" && r.State == "answered" && (r.AnsweredIn == "" || !strings.Contains(r.Reason, b.request.ID) || !strings.Contains(r.Reason, tc.reason)) {
					t.Errorf("the closed wait = %+v, want it answered on the card it names, saying %q", r, tc.reason)
				}
			}
		})
	}
}

// A goblin's open step card is found on the board's record and in its inbox
// before the board takes it, and a closed card, another goblin's or one the
// CFO filed for it is not.
func TestOpenStepCardFindsTheGoblinsOpenCard(t *testing.T) {
	// Arrange
	b := stepCard(t, func(r *CredentialRequest) { r.By = "goblin" })
	byCFO := CredentialRequest{ID: "cred-2222222222222222", Identity: strings.Repeat("e", 64), By: "cfo", Task: "task-4", Project: "throwaway", Why: "Tick the App Group", Link: "https://developer.apple.com/account"}
	if _, err := b.service.acceptCredentialRequest(byCFO); err != nil {
		t.Fatal(err)
	}
	spooled := CredentialRequest{ID: "cred-1111111111111111", Identity: strings.Repeat("d", 64), By: "goblin", Task: "task-2", Project: "throwaway", Why: "Tick the App Group", Link: "https://developer.apple.com/account"}
	if err := spoolCredentialRequest(b.home.State, spooled); err != nil {
		t.Fatal(err)
	}

	// Act
	onBoard, boardErr := OpenStepCard(b.home.State, "task-1")
	inInbox, inboxErr := OpenStepCard(b.home.State, "task-2")
	none, noneErr := OpenStepCard(b.home.State, "task-3")
	cfoCard, cfoErr := OpenStepCard(b.home.State, "task-4")
	if _, err := b.service.closeCredential(b.request.ID, "", "done", "cfo", ""); err != nil {
		t.Fatal(err)
	}
	b.service.credentialWork.Wait()
	closed, closedErr := OpenStepCard(b.home.State, "task-1")

	// Assert
	if err := errors.Join(boardErr, inboxErr, noneErr, cfoErr, closedErr); err != nil {
		t.Fatal(err)
	}
	if onBoard != b.request.ID || inInbox != spooled.ID || none != "" || cfoCard != "" || closed != "" {
		t.Fatalf("open cards = %q, %q, %q, %q, %q after Done; want %s, %s and none for the rest", onBoard, inInbox, none, cfoCard, closed, b.request.ID, spooled.ID)
	}
}
