package tickets

import (
	"slices"
	"strings"
	"time"
)

// knownBots are agent accounts GitHub types as ordinary users, so neither its
// bot typing nor a [bot] suffix gives them away. They are never people.
var knownBots = []string{"claude", "copilot", "copilot-swe-agent", "cursoragent", "devin-ai-integration", "github-actions", "dependabot"}

// isBot reports whether an actor is an automated account: typed as a bot by
// GitHub, named with the [bot] suffix, or one of knownBots.
func isBot(actor Actor) bool {
	if actor.IsBot {
		return true
	}
	for _, name := range []string{actor.Login, actor.Name} {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if strings.HasSuffix(name, "[bot]") || slices.Contains(knownBots, name) {
			return true
		}
	}
	return false
}

// isOverlord reports whether an actor is the account gh is signed in as. A
// commit whose email links to no account carries only a git name, which is
// his when it is his profile name or his login.
func isOverlord(actor, viewer Actor) bool {
	if actor.Login != "" {
		return strings.EqualFold(actor.Login, viewer.Login)
	}
	name := strings.TrimSpace(actor.Name)
	return name != "" && (strings.EqualFold(name, viewer.Login) || strings.EqualFold(name, strings.TrimSpace(viewer.Name)))
}

// isPerson reports whether an actor counts as someone else working in the
// repository: neither the Overlord nor a bot, and named at all.
func isPerson(actor, viewer Actor) bool {
	return (actor.Login != "" || strings.TrimSpace(actor.Name) != "") && !isBot(actor) && !isOverlord(actor, viewer)
}

// contributors tallies every person who did something within
// CollaborationWindow, the most recently active first.
func contributors(activity Activity, now time.Time) []Contributor {
	since := now.Add(-CollaborationWindow)
	byKey := map[string]*Contributor{}
	var order []string
	tally := func(actor Actor, at time.Time) *Contributor {
		if at.Before(since) || !isPerson(actor, activity.Viewer) {
			return nil
		}
		key := "login:" + strings.ToLower(actor.Login)
		if actor.Login == "" {
			key = "name:" + strings.ToLower(strings.TrimSpace(actor.Name))
		}
		contributor, ok := byKey[key]
		if !ok {
			contributor = &Contributor{Login: actor.Login}
			if actor.Login == "" {
				contributor.Name = strings.TrimSpace(actor.Name)
			}
			byKey[key] = contributor
			order = append(order, key)
		}
		if contributor.AvatarURL == "" {
			contributor.AvatarURL = actor.AvatarURL
		}
		if at.After(contributor.LastActive) {
			contributor.LastActive = at
		}
		return contributor
	}
	for _, event := range activity.Events {
		contributor := tally(event.Author, event.At)
		if contributor == nil {
			continue
		}
		switch event.Kind {
		case EventCommit:
			contributor.Commits++
		case EventIssue:
			contributor.Issues++
		case EventPullRequest:
			contributor.PullRequests++
		}
	}
	for _, branch := range activity.Branches {
		if branch.Name == activity.DefaultBranch {
			continue
		}
		if contributor := tally(branch.Author, branch.CommittedAt); contributor != nil {
			contributor.Branches++
		}
	}
	result := make([]Contributor, 0, len(order))
	for _, key := range order {
		result = append(result, *byKey[key])
	}
	slices.SortStableFunc(result, func(a, b Contributor) int { return b.LastActive.Compare(a.LastActive) })
	return result
}
