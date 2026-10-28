// Package waiting reads what waits on the Overlord from a board snapshot, as
// the board's page alerts it: the desktop window's notifications and the
// pushes to his Home Screen app tell the same items in the same words.
package waiting

import (
	"encoding/json"
	"strings"
	"time"
)

// cfoQuietAfter is how long the CFO has a goblin's question to itself; past
// it the question waits on the Overlord too, as the board's page counts it
// (CFO_QUIET_AFTER_MS).
const cfoQuietAfter = 10 * time.Minute

// Item is one thing on the board that asks the Overlord something: its
// alert's key as the board's page names it, the item's id with the
// publishing it stands for, so the page and the window raise one
// notification for it; who asks; and what it asks, on one plain line.
// IsQuiet marks one the page never alerts: a goblin's question the CFO has
// left, which it counts, and an item open but not waiting on him, which
// IsOpenOnly marks and it does not count, so that once it waits on him it is
// not new.
type Item struct {
	ID         string `json:"id"`
	Asker      string `json:"asker"`
	Text       string `json:"text"`
	IsQuiet    bool   `json:"is_quiet"`
	IsOpenOnly bool   `json:"is_open_only"`
}

// Look is what the supervisor answers the desktop window's look for what
// waits: its instance and the items.
type Look struct {
	Instance string `json:"instance"`
	Items    []Item `json:"items"`
}

// Items lists what a board snapshot holds that asks the Overlord
// something, which is what the board's page counts and alerts, each in the
// asker's words as the page says it: a pending question; an open review item,
// open only when he sent a revision on its page, which waits on its goblin,
// or it is a goblin's wait that follows a decision still pending, which the
// decision stands for; a command ready or running that no credential card or Remote
// access setting opened, since that terminal shows there; an open request for
// credentials, by its project alone, so the credential names stay out of a
// notification; and a browser sign-in under way. A question that names a
// task is a goblin's, which the CFO answers: it is listed only once the CFO
// has left it cfoQuietAfter, by the snapshot's time, and then quiet, as the
// page counts it but never alerts it. Nothing else on the
// board, such as a goblin blocked, failed or done, is an item. It names the
// supervisor's instance too, which every request to the board that changes
// something carries.
func Items(snapshot []byte) (string, []Item, error) {
	var view struct {
		Instance string    `json:"instance"`
		At       time.Time `json:"at"`
		Tasks    []struct {
			ID, Title string
		} `json:"tasks"`
		Questions []struct {
			ID, Text, Status, Task string
			CreatedAt              string `json:"created_at"`
		} `json:"questions"`
		Reviews []struct {
			ID, Title, State, Task, Lavish, Follows string
			CreatedAt                               string  `json:"created_at"`
			RevisingSince                           *string `json:"revising_since"`
		} `json:"reviews"`
		Runs []struct {
			ID, Title, State, Task string
			CredentialRequest      string `json:"credential_request"`
			RemoteFix              string `json:"remote_fix"`
			CreatedAt              string `json:"created_at"`
			Update                 *struct {
				From, To string
			} `json:"update"`
		} `json:"runs"`
		Credentials []struct {
			ID, Project, State, Task, Why string
			Names, Plain                  []string
			CreatedAt                     string `json:"created_at"`
		} `json:"credentials"`
		BrowserSignIns []struct {
			ID, Task, State, Provider, Project string
			CreatedAt                          string `json:"created_at"`
		} `json:"browser_signins"`
	}
	if err := json.Unmarshal(snapshot, &view); err != nil {
		return "", nil, err
	}
	// A goblin speaks by its title, as its card names it, and the CFO for
	// what names no goblin.
	speaker := func(task string) string {
		if task == "" {
			return "CFO"
		}
		for _, t := range view.Tasks {
			if t.ID == task {
				if name := withoutHarness(t.Title); name != "" {
					return name
				}
			}
		}
		return task
	}
	asker := func(task string) string {
		if task == "" {
			return "The CFO"
		}
		return shortened(speaker(task), nameLimit)
	}
	item := func(key, createdAt, task, text string) Item {
		return Item{ID: key + "@" + createdAt, Asker: speaker(task), Text: shortened(text, textLimit)}
	}
	var items []Item
	pending := map[string]bool{}
	for _, q := range view.Questions {
		if q.Status == "pending" {
			pending[q.ID] = true
		}
	}
	for _, q := range view.Questions {
		asked, err := time.Parse(time.RFC3339, q.CreatedAt)
		isLeftByTheCFO := err == nil && !view.At.IsZero() && view.At.Sub(asked) >= cfoQuietAfter
		if q.Status != "pending" || q.Task != "" && !isLeftByTheCFO {
			continue
		}
		question := item("question:"+q.ID, q.CreatedAt, q.Task, asker(q.Task)+" asks: "+lead(q.Text))
		question.IsQuiet = q.Task != ""
		items = append(items, question)
	}
	for _, r := range view.Reviews {
		if r.State != "open" {
			continue
		}
		review := item("review:"+r.ID, r.CreatedAt, r.Task, asker(r.Task)+" wants your review: "+plain(r.Title))
		// A goblin's wait on him reads as its reason, without the queue's
		// prefix or its page's link.
		if r.Task != "" && strings.HasPrefix(r.ID, "waiting-"+r.Task+"-") {
			reason := waitPrefix.ReplaceAllString(r.Title, "")
			if r.Lavish != "" {
				reason = strings.Replace(reason, " (page "+r.Lavish+")", "", 1)
			}
			review = item("review:"+r.ID, r.CreatedAt, r.Task, asker(r.Task)+" is waiting on you: "+plain(reason))
		}
		review.IsOpenOnly = r.RevisingSince != nil || r.Follows != "" && pending[r.Follows]
		review.IsQuiet = review.IsOpenOnly
		items = append(items, review)
	}
	for _, r := range view.Runs {
		if r.State != "ready" && r.State != "running" || r.CredentialRequest != "" || r.RemoteFix != "" {
			continue
		}
		switch {
		case r.Update != nil:
			release := item("run:"+r.ID, r.CreatedAt, "", "Code Goblins "+r.Update.To+" is ready: update from "+r.Update.From)
			release.Asker = "Code Goblins"
			items = append(items, release)
		case r.Task != "":
			items = append(items, item("run:"+r.ID, r.CreatedAt, r.Task, asker(r.Task)+" asks you to run a command: "+plain(r.Title)))
		default:
			items = append(items, item("run:"+r.ID, r.CreatedAt, "", "A command waits for you to run it: "+r.Title))
		}
	}
	// A card that asks for no value is a step he takes, by what it is for.
	for _, c := range view.Credentials {
		switch {
		case c.State != "open":
		case len(c.Names)+len(c.Plain) == 0:
			items = append(items, item("credential:"+c.ID, c.CreatedAt, c.Task, asker(c.Task)+" needs you for a step: "+plain(c.Why)))
		default:
			items = append(items, item("credential:"+c.ID, c.CreatedAt, c.Task, asker(c.Task)+" asks for credentials for "+c.Project))
		}
	}
	for _, b := range view.BrowserSignIns {
		if b.State == "ready" || b.State == "opening" || b.State == "open" {
			items = append(items, item("browser-signin:"+b.ID, b.CreatedAt, b.Task, asker(b.Task)+" asks: Sign in to "+b.Provider+" for "+b.Project))
		}
	}
	return view.Instance, items, nil
}
