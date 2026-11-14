package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/fpresta0607/code-goblins/internal/actionsbilling"
)

type BillingRepair struct {
	Month   string `json:"month"`
	Account string `json:"account"`
}

var billingAccount = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,38}$`)

var billingBoard = boardAsker("Changing GitHub billing access is the Supreme Overlord's", ": he allows it on his own board on this PC", "Allow billing access in the Code Goblins window on this PC.", reopenTheWindow+"allow it there.")

func (h *HTTP) repairActionsSpend(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Month  string `json:"month"`
		Action string `json:"action"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1025))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		apiError(w, http.StatusBadRequest, "Choose the billing action shown on this panel.")
		return
	}
	_, monthError := time.Parse("2006-01", input.Month)
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF || monthError != nil || !isActionsSpendMonth(time.Now(), input.Month) {
		apiError(w, http.StatusBadRequest, "Choose this or last month's billing action.")
		return
	}
	// Retry is a forced read, which GET /api/actions-spend serves on any
	// board; only consent is his own board's.
	if input.Action == "retry" {
		if h.Service.actionsSpendUsage(time.Now(), input.Month, h.Service.billingRuns()).Problem == nil {
			apiError(w, http.StatusConflict, "The billing reading has no problem to retry. Refresh the panel.")
			return
		}
		respond(w, http.StatusOK, h.Service.refreshActionsSpend(r.Context(), time.Now(), input.Month, false))
		return
	}
	if input.Action != "allow" {
		apiError(w, http.StatusBadRequest, "Choose the billing action shown on this panel.")
		return
	}
	if _, err := h.Service.overlordsBoard(r, h.Host, time.Now(), billingBoard); err != nil {
		h.refuse(w, http.StatusForbidden, "billing", "A GitHub billing repair was refused", "", err)
		return
	}
	reading := h.Service.actionsSpendUsage(time.Now(), input.Month, h.Service.billingRuns())
	if reading.Problem == nil || reading.Problem.Action != "allow" {
		apiError(w, http.StatusConflict, "The billing action changed. Refresh the panel.")
		return
	}
	if err := h.Service.prepareBillingRepair(input.Month, *reading.Problem); err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, h.Service.actionsSpendUsage(time.Now(), input.Month, h.Service.billingRuns()))
}

func (s *Service) prepareBillingRepair(month string, problem actionsbilling.Problem) error {
	s.runRequests.Lock()
	defer s.runRequests.Unlock()
	if s.Options.GitHub == nil || problem.Credential != "gh" || s.Options.GitHub.CredentialSource() != "gh" {
		return errors.New("Update the selected environment credential on this PC; refreshing the CLI login will not change it.")
	}
	if problem.Action != "allow" || !billingAccount.MatchString(problem.Account) {
		return errors.New("Recheck the selected GitHub account before allowing billing access.")
	}
	for _, run := range s.Store.Snapshot().Runs {
		if run.Billing != nil && (run.State == "ready" || run.State == "running") {
			if run.Billing.Account == problem.Account {
				return nil
			}
			return errors.New("Complete or cancel the pending billing consent in Command Center before changing accounts.")
		}
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return errors.New("Billing consent is unavailable.")
	}
	identity := sha256.Sum256([]byte(s.Instance + "\nbilling"))
	now := time.Now().UTC()
	command := "$ErrorActionPreference = 'Stop'\nif ($env:GH_TOKEN -or $env:GITHUB_TOKEN) { throw 'An environment credential takes precedence. Update it instead of refreshing the CLI login.' }\n"
	command += "$billingAccount = & gh api --hostname github.com user --jq .login\nif ($LASTEXITCODE -ne 0 -or $billingAccount -ne '" + problem.Account + "') { throw 'The selected GitHub account changed. Recheck billing before granting access.' }\n"
	command += "& gh auth refresh --hostname github.com --scopes user\n"
	command += "exit $LASTEXITCODE\n"
	title := "Allow billing access for " + problem.Account + " (GitHub user scope)"
	_, err := s.recordBoardRun(Run{ID: "actions-billing-" + hex.EncodeToString(nonce[:]), Identity: hex.EncodeToString(identity[:]), Title: title, Shell: "powershell", Command: command, Cwd: s.Store.Home.Root, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime), Billing: &BillingRepair{Month: month, Account: problem.Account}})
	return err
}

// refreshActionsSpend reads month again unless a read is in flight or a Retry
// read it less than 10 s ago. A Retry that reads again after GitHub refused
// gh's login clears the client's refused-token wait first, so a login renewed
// with gh auth login is read at once, and gh starts at most once per read,
// never once per click (#2003).
func (s *Service) refreshActionsSpend(ctx context.Context, now time.Time, month string, afterConsent bool) actionsbilling.Reading {
	s.mu.Lock()
	entry := s.actionsSpendReadings[month]
	isRefusedLoginRetried := false
	if entry != nil && entry.done == nil && (afterConsent || now.Sub(entry.retried) >= 10*time.Second) {
		entry.started, entry.retried = time.Time{}, now
		isRefusedLoginRetried = !afterConsent && entry.reading.Problem != nil && entry.reading.Problem.Kind == "authentication"
	}
	s.mu.Unlock()
	if isRefusedLoginRetried && s.Options.GitHub != nil {
		s.Options.GitHub.InvalidateCLIToken()
	}
	if afterConsent {
		return s.readActionsSpendMonth(ctx, ctx, now, month)
	}
	return s.readActionsSpendMonth(ctx, s.lifetime, now, month)
}

// finishBillingRepair drops the CLI token a successful consent replaced and
// rereads the month once, beside the cycle that ended the run, so a slow gh or
// GitHub never holds the supervisor's loop.
func (s *Service) finishBillingRepair(run Run, state string) {
	defer s.notify()
	if state != "succeeded" || s.Options.GitHub == nil || s.Options.GitHub.CredentialSource() != "gh" {
		return
	}
	// New reads use the refreshed token while earlier reads finish.
	s.Options.GitHub.InvalidateCLIToken()
	s.mu.Lock()
	for _, cached := range s.actionsSpendReadings {
		cached.started = time.Time{}
	}
	entry := s.actionsSpendReadings[run.Billing.Month]
	var done chan struct{}
	if entry != nil {
		done = entry.done
	}
	s.mu.Unlock()
	s.credentialWork.Add(1)
	go func() {
		defer s.credentialWork.Done()
		bounded, cancel := context.WithTimeout(s.lifetime, 35*time.Second)
		defer cancel()
		if done != nil {
			select {
			case <-done:
			case <-bounded.Done():
				return
			}
		}
		s.refreshActionsSpend(bounded, time.Now(), run.Billing.Month, true)
	}()
}
