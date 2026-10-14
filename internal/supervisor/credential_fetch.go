package supervisor

import (
	"context"
	"errors"
	"net/http"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

// CredentialFetch is what a request's card says of fetching its keys: the
// provider whose reviewed key page reads them, the login a fetch reads with,
// the names it reads, a fetch running now, and what stopped the last one.
type CredentialFetch struct {
	Provider string   `json:"provider"`
	Login    string   `json:"login"`
	Names    []string `json:"names"`
	State    string   `json:"state,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

// fetchOffer is what a request for names of project, whose keys are on the
// page link, offers to fetch: the names its reviewed recipe reads, by the
// provider and the login project fetches with, or nil when it reads none.
func (b *browserBroker) fetchOffer(link string, names []string, project string) *CredentialFetch {
	provider, keys, err := b.recipes.Fetch(link, names)
	if err != nil {
		return nil
	}
	login := project
	if manifest, err := auth.LoadManifest(b.data, project); err == nil {
		if declared, found := manifest.LoginFor(provider.ID); found {
			login = declared.Account
		}
	}
	offer := &CredentialFetch{Provider: provider.Name, Login: login}
	for _, key := range keys {
		offer.Names = append(offer.Names, key.Name)
	}
	return offer
}

// fetchFromCard starts the fetch a request's card offers, on his click on
// the board on this PC, for the goblin the request asks for. It answers at
// once, and the card shows the fetch running, then its keys stored, his
// sign-in card, or why it stopped.
func (h *HTTP) fetchFromCard(w http.ResponseWriter, r *http.Request) {
	if problem := loopbackProblem(r, h.Host); problem != "" {
		apiError(w, http.StatusForbidden, problem)
		return
	}
	var input struct {
		ID         string `json:"id"`
		Generation string `json:"generation"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, "Send the request's ID and generation.")
		return
	}
	err := h.Service.startCardFetch(input.ID, input.Generation)
	var refusal credentialRefusal
	switch {
	case errors.As(err, &refusal):
		respond(w, refusal.status, refusal)
	case err != nil:
		apiError(w, http.StatusInternalServerError, err.Error())
	default:
		respond(w, http.StatusAccepted, struct {
			State string `json:"state"`
		}{"fetching"})
	}
}

// startCardFetch marks the request's fetch running and runs it in the
// background. A request whose fetch runs already is left to it.
func (s *Service) startCardFetch(id, generation string) error {
	s.credentialSaves.Lock()
	defer s.credentialSaves.Unlock()
	request, err := s.openCredential(id, generation)
	if err != nil {
		return err
	}
	if request.Fetch == nil || request.Task == "" {
		return credentialRefusal{status: http.StatusConflict, Message: "No reviewed page reads these keys. Paste them instead."}
	}
	if request.Fetch.State == "fetching" {
		return nil
	}
	if _, err := s.Store.updateCredential(id, func(r *CredentialRequest) { r.Fetch.State, r.Fetch.Reason = "fetching", "" }); err != nil {
		return err
	}
	s.notify()
	s.credentialWork.Add(1)
	go s.cardFetch(request)
	return nil
}

// cardFetch runs the fetch a card started and records on the card how it
// ended: its keys stored, which closes the request as a save does, his
// sign-in card filed, which the Command Center shows by itself, or why it
// stopped, which the CFO hears too.
func (s *Service) cardFetch(request CredentialRequest) {
	defer s.credentialWork.Done()
	ctx, cancel := context.WithTimeout(context.Background(), browserFetchTimeout)
	defer cancel()
	_, err := s.fetchCredential(ctx, BrowserRequest{Action: "fetch", Task: request.Task, Request: request.ID})
	reason := ""
	if err != nil {
		reason = bounded(err.Error(), 300)
		s.credentialNotice(request, "the fetch his card started for "+request.ID+" stopped: "+reason+"; the card still takes the keys pasted")
	}
	if _, err := s.Store.updateCredential(request.ID, func(r *CredentialRequest) {
		if r.Fetch != nil {
			r.Fetch.State, r.Fetch.Reason = "", reason
		}
	}); err != nil {
		s.publish(err)
	}
	s.notify()
}

// stopCardFetches records every fetch a card started as stopped, as a
// supervisor that starts runs none.
func (s *Store) stopCardFetches() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	isChanged := false
	for index := range s.db.Credentials {
		if fetch := s.db.Credentials[index].Fetch; fetch != nil && fetch.State == "fetching" {
			fetch.State, fetch.Reason, isChanged = "", "The supervisor restarted during the fetch. Fetch again.", true
		}
	}
	if !isChanged {
		return nil
	}
	return s.save()
}
