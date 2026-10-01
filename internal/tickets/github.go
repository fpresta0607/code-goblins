package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// GitHub reads a repository's activity through the gh the machine is signed
// in with. The account gh is signed in as is the Overlord.
//
// A read is one GraphQL query (about three points of the hourly budget every
// goblin, gate and the CFO share), more pages of the same query only for a
// connection that has them, and REST calls only for a pull request with more
// than a hundred files or a teammate's recent branch that no pull request
// heads.
type GitHub struct {
	Commands execx.Runner
}

// The read's bounds: rounds of follow-up pages, and branches compared one by
// one. What lies past either is named in Activity.Unread.
const (
	maxPageRounds     = 10
	maxBranchCompares = 30
)

var githubRemote = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:)([\w.-]+/[\w.-]+?)(?:\.git)?/?$`)

// RepositoryOf names the GitHub repository a checkout's origin remote is.
func (g GitHub) RepositoryOf(ctx context.Context, checkout string) (string, error) {
	result, err := g.Commands.Run(ctx, execx.Request{Name: "git", Args: []string{"-C", checkout, "remote", "get-url", "origin"}})
	if err != nil {
		return "", fmt.Errorf("read the origin remote of %s: %w", checkout, err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("read the origin remote of %s: %s", checkout, strings.TrimSpace(string(result.Stderr)))
	}
	remote := strings.TrimSpace(string(result.Stdout))
	match := githubRemote.FindStringSubmatch(remote)
	if match == nil {
		return "", fmt.Errorf("origin %s of %s is not a GitHub repository", remote, checkout)
	}
	return match[1], nil
}

// activityQuery reads everything in one round; later rounds include only the
// connections that still have pages.
const activityQuery = `query($owner:String!,$name:String!,$since:GitTimestamp!,$first:Boolean!,$issues:Boolean!,$issuesAfter:String,$pulls:Boolean!,$pullsAfter:String,$refs:Boolean!,$refsAfter:String){
viewer @include(if:$first){login name}
repository(owner:$owner,name:$name){
nameWithOwner
defaultBranchRef @include(if:$first){name target{oid ... on Commit{history(first:100,since:$since){nodes{committedDate author{name user{login avatarUrl}}}}}}}
recentIssues:issues(first:100,orderBy:{field:CREATED_AT,direction:DESC}) @include(if:$first){nodes{number createdAt author{__typename login avatarUrl}}}
recentPullRequests:pullRequests(first:100,orderBy:{field:CREATED_AT,direction:DESC}) @include(if:$first){nodes{number createdAt author{__typename login avatarUrl}}}
issues(states:OPEN,first:100,after:$issuesAfter,orderBy:{field:CREATED_AT,direction:DESC}) @include(if:$issues){pageInfo{hasNextPage endCursor} nodes{number title body url createdAt author{__typename login avatarUrl} assignees(first:10){nodes{login}} labels(first:20){nodes{name}}}}
pullRequests(states:OPEN,first:50,after:$pullsAfter,orderBy:{field:CREATED_AT,direction:DESC}) @include(if:$pulls){pageInfo{hasNextPage endCursor} nodes{number title url isDraft createdAt updatedAt headRefName isCrossRepository changedFiles author{__typename login avatarUrl} files(first:100){nodes{path}}}}
refs(refPrefix:"refs/heads/",first:100,after:$refsAfter) @include(if:$refs){pageInfo{hasNextPage endCursor} nodes{name target{oid ... on Commit{committedDate author{name user{login avatarUrl}}}}}}
}
}`

type graphActor struct {
	Typename  string `json:"__typename"`
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
}

// actor reads an account; a deleted account comes back null and is nobody.
func (a *graphActor) actor() Actor {
	if a == nil {
		return Actor{}
	}
	return Actor{Login: a.Login, AvatarURL: a.AvatarURL, IsBot: a.Typename == "Bot"}
}

type gitActor struct {
	Name string `json:"name"`
	User *struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatarUrl"`
	} `json:"user"`
}

func (a gitActor) actor() Actor {
	if a.User == nil {
		return Actor{Name: a.Name}
	}
	return Actor{Login: a.User.Login, Name: a.Name, AvatarURL: a.User.AvatarURL}
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type openedNodes struct {
	Nodes []struct {
		Number    int         `json:"number"`
		CreatedAt time.Time   `json:"createdAt"`
		Author    *graphActor `json:"author"`
	} `json:"nodes"`
}

type activityResponse struct {
	Data struct {
		Viewer *struct {
			Login string `json:"login"`
			Name  string `json:"name"`
		} `json:"viewer"`
		Repository *struct {
			NameWithOwner    string `json:"nameWithOwner"`
			DefaultBranchRef *struct {
				Name   string `json:"name"`
				Target struct {
					Oid     string `json:"oid"`
					History struct {
						Nodes []struct {
							CommittedDate time.Time `json:"committedDate"`
							Author        gitActor  `json:"author"`
						} `json:"nodes"`
					} `json:"history"`
				} `json:"target"`
			} `json:"defaultBranchRef"`
			RecentIssues       *openedNodes `json:"recentIssues"`
			RecentPullRequests *openedNodes `json:"recentPullRequests"`
			Issues             *struct {
				PageInfo pageInfo `json:"pageInfo"`
				Nodes    []struct {
					Number    int         `json:"number"`
					Title     string      `json:"title"`
					Body      string      `json:"body"`
					URL       string      `json:"url"`
					CreatedAt time.Time   `json:"createdAt"`
					Author    *graphActor `json:"author"`
					Assignees struct {
						Nodes []struct {
							Login string `json:"login"`
						} `json:"nodes"`
					} `json:"assignees"`
					Labels struct {
						Nodes []struct {
							Name string `json:"name"`
						} `json:"nodes"`
					} `json:"labels"`
				} `json:"nodes"`
			} `json:"issues"`
			PullRequests *struct {
				PageInfo pageInfo `json:"pageInfo"`
				Nodes    []struct {
					Number            int         `json:"number"`
					Title             string      `json:"title"`
					URL               string      `json:"url"`
					IsDraft           bool        `json:"isDraft"`
					CreatedAt         time.Time   `json:"createdAt"`
					UpdatedAt         time.Time   `json:"updatedAt"`
					HeadRefName       string      `json:"headRefName"`
					IsCrossRepository bool        `json:"isCrossRepository"`
					ChangedFiles      int         `json:"changedFiles"`
					Author            *graphActor `json:"author"`
					Files             struct {
						Nodes []struct {
							Path string `json:"path"`
						} `json:"nodes"`
					} `json:"files"`
				} `json:"nodes"`
			} `json:"pullRequests"`
			Refs *struct {
				PageInfo pageInfo `json:"pageInfo"`
				Nodes    []struct {
					Name   string `json:"name"`
					Target struct {
						Oid           string    `json:"oid"`
						CommittedDate time.Time `json:"committedDate"`
						Author        gitActor  `json:"author"`
					} `json:"target"`
				} `json:"nodes"`
			} `json:"refs"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// Read reads one repository's activity as of now.
func (g GitHub) Read(ctx context.Context, repository string, now time.Time) (Activity, error) {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" {
		return Activity{}, fmt.Errorf("repository %q is not owner/name", repository)
	}
	activity := Activity{Repository: repository}
	since := now.Add(-CollaborationWindow).UTC().Format(time.RFC3339)
	include := map[string]bool{"first": true, "issues": true, "pulls": true, "refs": true}
	cursors := map[string]string{}
	pullFiles := map[int]int{}
	for round := 0; include["issues"] || include["pulls"] || include["refs"]; round++ {
		if round == maxPageRounds {
			for _, connection := range []string{"issues", "pulls", "refs"} {
				if include[connection] {
					activity.Unread = append(activity.Unread, fmt.Sprintf("open %s past the first %d pages", connectionNoun[connection], maxPageRounds))
				}
			}
			break
		}
		response, err := g.query(ctx, owner, name, since, include, cursors)
		if err != nil {
			return Activity{}, err
		}
		data := response.Data
		if include["first"] {
			if data.Viewer != nil {
				activity.Viewer = Actor{Login: data.Viewer.Login, Name: data.Viewer.Name}
			}
			readFirstRound(&activity, response)
		}
		repo := data.Repository
		include["first"] = false
		if include["issues"] {
			for _, node := range repo.Issues.Nodes {
				issue := Issue{Number: node.Number, Title: node.Title, Body: node.Body, URL: node.URL, CreatedAt: node.CreatedAt, Author: node.Author.actor(), Assignees: []string{}, Labels: []string{}}
				for _, assignee := range node.Assignees.Nodes {
					issue.Assignees = append(issue.Assignees, assignee.Login)
				}
				for _, label := range node.Labels.Nodes {
					issue.Labels = append(issue.Labels, label.Name)
				}
				activity.Issues = append(activity.Issues, issue)
			}
			include["issues"], cursors["issuesAfter"] = repo.Issues.PageInfo.HasNextPage, repo.Issues.PageInfo.EndCursor
		}
		if include["pulls"] {
			for _, node := range repo.PullRequests.Nodes {
				pull := PullRequest{Number: node.Number, Title: node.Title, URL: node.URL, IsDraft: node.IsDraft, HeadRef: node.HeadRefName, FromFork: node.IsCrossRepository, CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt, Author: node.Author.actor(), Files: []string{}}
				for _, file := range node.Files.Nodes {
					pull.Files = append(pull.Files, file.Path)
				}
				pullFiles[node.Number] = node.ChangedFiles
				activity.PullRequests = append(activity.PullRequests, pull)
			}
			include["pulls"], cursors["pullsAfter"] = repo.PullRequests.PageInfo.HasNextPage, repo.PullRequests.PageInfo.EndCursor
		}
		if include["refs"] {
			for _, node := range repo.Refs.Nodes {
				activity.Branches = append(activity.Branches, Branch{Name: node.Name, Head: node.Target.Oid, CommittedAt: node.Target.CommittedDate, Author: node.Target.Author.actor()})
			}
			include["refs"], cursors["refsAfter"] = repo.Refs.PageInfo.HasNextPage, repo.Refs.PageInfo.EndCursor
		}
	}
	for i, pull := range activity.PullRequests {
		if pullFiles[pull.Number] <= len(pull.Files) {
			continue
		}
		files, err := g.lines(ctx, "api", "--paginate", fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", repository, pull.Number), "--jq", ".[].filename")
		if err != nil {
			return Activity{}, fmt.Errorf("read the files of pull request %d: %w", pull.Number, err)
		}
		activity.PullRequests[i].Files = files
	}
	if err := g.compareBranches(ctx, &activity, now); err != nil {
		return Activity{}, err
	}
	return activity, nil
}

var connectionNoun = map[string]string{"issues": "issues", "pulls": "pull requests", "refs": "branches"}

// readFirstRound reads what only the first round asks for: the default
// branch and the dated acts that show who works here.
func readFirstRound(activity *Activity, response activityResponse) {
	repo := response.Data.Repository
	if branch := repo.DefaultBranchRef; branch != nil {
		activity.DefaultBranch, activity.DefaultHead = branch.Name, branch.Target.Oid
		for _, node := range branch.Target.History.Nodes {
			activity.Events = append(activity.Events, Event{Kind: EventCommit, Author: node.Author.actor(), At: node.CommittedDate})
		}
	}
	for kind, opened := range map[string]*openedNodes{EventIssue: repo.RecentIssues, EventPullRequest: repo.RecentPullRequests} {
		if opened == nil {
			continue
		}
		for _, node := range opened.Nodes {
			activity.Events = append(activity.Events, Event{Kind: kind, Number: node.Number, Author: node.Author.actor(), At: node.CreatedAt})
		}
	}
}

// compareBranches reads the files of each listed branch no pull request
// heads, comparing commit ids so no branch name needs escaping.
func (g GitHub) compareBranches(ctx context.Context, activity *Activity, now time.Time) error {
	if activity.DefaultHead == "" {
		return nil
	}
	names := BranchesToCompare(*activity, now)
	if len(names) > maxBranchCompares {
		activity.Unread = append(activity.Unread, fmt.Sprintf("the changed files of %d branches past the first %d", len(names)-maxBranchCompares, maxBranchCompares))
		names = names[:maxBranchCompares]
	}
	for _, name := range names {
		for i := range activity.Branches {
			branch := &activity.Branches[i]
			if branch.Name != name {
				continue
			}
			files, err := g.lines(ctx, "api", fmt.Sprintf("repos/%s/compare/%s...%s", activity.Repository, activity.DefaultHead, branch.Head), "--jq", ".files[].filename")
			if err != nil {
				return fmt.Errorf("compare branch %s with %s: %w", name, activity.DefaultBranch, err)
			}
			branch.Files = files
		}
	}
	return nil
}

func (g GitHub) query(ctx context.Context, owner, name, since string, include map[string]bool, cursors map[string]string) (activityResponse, error) {
	args := []string{"api", "graphql", "-f", "query=" + activityQuery, "-f", "owner=" + owner, "-f", "name=" + name, "-f", "since=" + since}
	for _, flag := range []string{"first", "issues", "pulls", "refs"} {
		args = append(args, "-F", flag+"="+strconv.FormatBool(include[flag]))
	}
	for _, cursor := range []string{"issuesAfter", "pullsAfter", "refsAfter"} {
		connection := strings.TrimSuffix(cursor, "After")
		if include[connection] && cursors[cursor] != "" {
			args = append(args, "-f", cursor+"="+cursors[cursor])
		}
	}
	result, err := g.Commands.Run(ctx, execx.Request{Name: "gh", Args: args})
	if err != nil {
		return activityResponse{}, fmt.Errorf("gh api graphql: %w", err)
	}
	var response activityResponse
	decodeErr := json.Unmarshal(result.Stdout, &response)
	var messages []string
	for _, problem := range response.Errors {
		messages = append(messages, problem.Message)
	}
	if result.ExitCode != 0 {
		messages = append(messages, strings.TrimSpace(string(result.Stderr)))
		return activityResponse{}, fmt.Errorf("gh api graphql exited %d: %s", result.ExitCode, strings.Join(messages, "; "))
	}
	if decodeErr != nil {
		return activityResponse{}, fmt.Errorf("decode the GitHub read: %w", decodeErr)
	}
	if response.Data.Repository == nil {
		if len(messages) > 0 {
			return activityResponse{}, errors.New(strings.Join(messages, "; "))
		}
		return activityResponse{}, fmt.Errorf("repository %s/%s is not visible to gh", owner, name)
	}
	repo := response.Data.Repository
	if include["issues"] && repo.Issues == nil || include["pulls"] && repo.PullRequests == nil || include["refs"] && repo.Refs == nil {
		if len(messages) == 0 {
			messages = []string{"no error given"}
		}
		return activityResponse{}, fmt.Errorf("GitHub left part of %s/%s unread: %s", owner, name, strings.Join(messages, "; "))
	}
	return response, nil
}

// lines runs gh and returns its non-empty output lines.
func (g GitHub) lines(ctx context.Context, args ...string) ([]string, error) {
	result, err := g.Commands.Run(ctx, execx.Request{Name: "gh", Args: args})
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("gh exited %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	var lines []string
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}
