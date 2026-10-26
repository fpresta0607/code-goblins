package supervisor

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func (s *Service) credentialGrantTask(card CredentialRequest) (state.TaskMeta, error) {
	meta, err := s.connectionTask(card.Task, card.TaskGeneration)
	if err != nil {
		return meta, err
	}
	if auth.ProjectName(meta.Project) != card.Project || card.By == "goblin" && goblinIdentity(meta) != card.Identity {
		return meta, errors.New("the card does not belong to this task's project and session")
	}
	return meta, nil
}

func (s *Service) grantSavedCredentials(card CredentialRequest, saved []string) string {
	if card.Task == "" || len(saved) == 0 {
		return ""
	}
	meta, err := s.credentialGrantTask(card)
	if err != nil {
		return "No service granted: " + err.Error()
	}
	manifest, err := auth.LoadManifest(s.Store.Home.Data, card.Project)
	if err != nil {
		return "No service granted: the project's service declarations could not be read."
	}
	var services []string
	for _, name := range saved {
		declared := card.Services[name]
		if len(declared) != 1 {
			return "No service granted: each saved name must belong to exactly one declared service."
		}
		matches := 0
		for _, service := range manifest.Services {
			reads := slices.Contains(service.Env, name)
			for _, aliases := range service.Aliases {
				reads = reads || slices.Contains(aliases, name)
			}
			if reads {
				matches++
				if service.Name != declared[0] {
					return "No service granted: the saved name's service declaration changed."
				}
			}
		}
		if matches != 1 {
			return "No service granted: the saved name's service declaration changed."
		}
		if !slices.Contains(services, declared[0]) {
			services = append(services, declared[0])
		}
	}
	refresher := spawn.AuthRefresher{StateDir: s.Store.Home.State, DataDir: s.Store.Home.Data, Panes: spawn.NativeLiveness{StateDir: s.Store.Home.State}, By: "the Overlord saving task card " + card.ID, Generation: card.TaskGeneration, Project: card.Project}
	if s.Options.Credentials != nil {
		refresher.Store, err = s.Options.Credentials()
		if err != nil {
			return "No service granted: the credential store could not be opened."
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), credentialRefreshTimeout)
	defer cancel()
	item, _, err := refresher.Grant(ctx, meta.ID, services)
	if err != nil && !spawn.ChangeStands(err) {
		return "No service granted: " + err.Error()
	}
	detail := "Granted " + strings.Join(services, ", ") + " to " + meta.ID + "."
	if err != nil {
		return detail + " " + err.Error()
	}
	if s.Options.CFO != nil {
		s.credentialWork.Add(1)
		go func() {
			defer s.credentialWork.Done()
			ctx, cancel := context.WithTimeout(context.Background(), credentialRefreshTimeout)
			defer cancel()
			if _, err := s.Options.CFO.SendGoblin(ctx, meta.ID, goblinIdentity(meta), "credentials refreshed: re-source "+item.Path); err != nil {
				s.credentialNotice(card, "The service was granted, but "+meta.ID+" could not be told to re-source auth.ps1.")
			}
		}()
	}
	return detail
}
