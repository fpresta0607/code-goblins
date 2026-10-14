package supervisor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/train"
)

// trainLanding names a pull request a merge train landed, for the supervisor
// to log as merged while AFK mode is on.
type trainLanding struct {
	Train string `json:"train"`
	URL   string `json:"url"`
}

// LogTrainLanding asks the supervisor to log the landing of a pull request by
// a merge train while AFK mode is on, from whichever process runs the train:
// a train started detached does not run under the registered CFO, which the
// log of a decision needs, so nothing of what it merged was logged. Nothing
// the caller says is logged: the supervisor reads the train's record for the
// landing and GitHub for the state of the pull request.
func LogTrainLanding(h home.Home, trainID, url string) error {
	return sendPipeRequest(h.State, runPipeRequest{Kind: "afk-train-landed", Train: &trainLanding{Train: trainID, URL: url}})
}

// logTrainLanding logs the landing the train's own record holds, as merged,
// with the run that proved it, once the pull request is read as merged on
// GitHub. It logs nothing while AFK mode is off. GitHub not answering leaves
// the record the train kept after it confirmed the merge itself to stand, and
// an answer that the pull request is not merged refuses the line.
func (s *Service) logTrainLanding(ctx context.Context, landing trainLanding) error {
	if !s.afkOn() {
		return nil
	}
	if landing.Train != filepath.Base(landing.Train) || strings.ContainsAny(landing.Train, "/\\:") {
		return errors.New("a train landing names a train by its id")
	}
	t, err := train.Read(s.Store.Home.State, landing.Train)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(t.Cars, func(car train.Car) bool { return car.URL == landing.URL && car.State == train.CarLanded })
	if i < 0 {
		return fmt.Errorf("the record of train %s does not hold %s landed", landing.Train, landing.URL)
	}
	if s.Options.PullRequestState != nil {
		if info, err := s.Options.PullRequestState(ctx, landing.URL); err == nil && info.State != "MERGED" {
			return errors.New("GitHub says " + landing.URL + " is " + info.State + ", not merged")
		}
	}
	return s.logAFKDecision(afk.Entry{Kind: afk.KindMerge, What: landing.URL, Link: landing.URL, Evidence: t.LandingEvidence(t.Cars[i]), Outcome: afk.OutcomeMerged})
}
