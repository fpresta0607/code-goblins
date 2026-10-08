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

// The read's bounds: pages per connection or pull request, and branches
// compared one by one. What lies past either is named in Activity.Unread.
const (
	maxPageRounds     = 10
	maxBranchCompares = 30
)

var githubRemote = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:)([\w.-]+/[\w.-]+?)(?:\.git)?/?$`)

// ErrNotGitHub says a checkout has no GitHub repository: it has no origin
// remote, or its origin is somewhere else.
var ErrNotGitHub = errors.New("not a GitHub repository")

// noSuchRemote is git remote get-url's exit code for a remote that does not
// exist.
const noSuchRemote = 2

// RepositoryOf names the GitHub repository a checkout's origin remote is.
func (g GitHub) RepositoryOf(ctx context.Context, checkout string) (string, error) {
	result, err := g.Commands.Run(ctx, execx.Request{Name: "git", Args: []string{"-C", checkout, "remote", "get-url", "origin"}})
	if err != nil {
		return "", fmt.Errorf("read the origin remote of %s: %w", checkout, err)
	}
	if result.ExitCode == noSuchRemote {
		return "", fmt.Errorf("read the origin remote of %s: %s: %w", checkout, strings.TrimSpace(string(result.Stderr)), ErrNotGitHub)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("read the origin remote of %s: %s", checkout, strings.TrimSpace(string(result.Stderr)))
	}
	remote := strings.TrimSpace(string(result.Stdout))
	match := githubRemote.FindStringSubmatch(remote)
	if match == nil {
		return "", fmt.Errorf("origin %s of %s is %w", remote, checkout, ErrNotGitHub)
	}
	return match[1], nil
}

// activityQuery reads everything in one round; later rounds include only the
// connections that still have pages.
const activityQuery = `query($owner:String!,$name:String!,$since:GitTimestamp!,$first:Boolean!,$issues:Boolean!,$issuesAfter:String,$pulls:Boolean!,$pullsAfter:String,$refs:Boolean!,$refsAfter:String){
viewer @include(if:$first){login name}
repository(owner:$owner,name:$name){
nameWithOwner isPrivate
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
			IsPrivate        bool   `json:"isPrivate"`
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

// Read reads one repository's activity as of now, all of it within ctx. A
// later page that fails names what it would have read in Activity.Unread,
// but a first page that fails fails the read.
func (g GitHub) Read(ctx context.Context, repository string, now time.Time) (Activity, error) {
	read, err := newReading(repository, now, "issues", "pulls", "refs")
	if err != nil {
		return Activity{}, err
	}
	for !read.isDone() {
		if err := g.next(ctx, read); err != nil {
			if read.include["first"] {
				return Activity{}, err
			}
			read.giveUp(err)
		}
	}
	g.compareBranches(ctx, &read.activity, now)
	return read.activity, nil
}

// OpenWork is the supervisor's overlap read of one repository, taken a few
// pages a pass: the account gh works as, the dated acts that show who works
// there, and every open issue and pull request with the files it changes.
// It reads no branch. The overlap read compares no branch's files, so no
// branch can meet a goblin's area, and GitHub lists branches by name, never
// by their last push: on 2026-10-08 paging through the 403 branches of
// fpresta0607/code-goblins under one deadline every ten minutes timed out
// pass after pass.
type OpenWork struct {
	read *reading
}

// StartOpenWork begins an overlap read of repository as of now. It asks
// GitHub nothing until Continue.
func StartOpenWork(repository string, now time.Time) (*OpenWork, error) {
	read, err := newReading(repository, now, "issues", "pulls")
	if err != nil {
		return nil, err
	}
	return &OpenWork{read: read}, nil
}

// Continue reads at most pages more pages of work, each on a deadline of its
// own, and returns how many it read. It stops at the first page that fails
// and returns why: work keeps its place, so the next Continue asks for that
// page again rather than starting over.
func (g GitHub) Continue(ctx context.Context, work *OpenWork, pages int, deadline time.Duration) (int, error) {
	read := 0
	for read < pages && !work.read.isDone() {
		page, cancel := context.WithTimeout(ctx, deadline)
		err := g.next(page, work.read)
		cancel()
		if err != nil {
			return read, err
		}
		read++
	}
	return read, nil
}

// Activity is what work has read so far.
func (w *OpenWork) Activity() Activity {
	return w.read.activity
}

// IsWhole reports whether work has read everything it reads.
func (w *OpenWork) IsWhole() bool {
	return w.read.isDone()
}

// reading is one read of a repository's activity, taken one gh call at a
// time: a round of the GraphQL query while a connection has pages, then a
// page of the files of each pull request that changes more than the hundred
// the query lists. A call that fails leaves the reading as it was, so its
// caller either asks again or gives up what the call was reading.
type reading struct {
	activity           Activity
	owner, name, since string
	include            map[string]bool
	cursors            map[string]string
	rounds             int
	changedFiles       map[int]int
	files              []pullFiles
}

// pullFiles is a pull request whose files are read page by page: where it is
// in the activity's pull requests, the files known, the pages read and the
// last page asked for.
type pullFiles struct {
	index, pages, last int
	known              map[string]bool
}

var connections = []string{"issues", "pulls", "refs"}

var connectionNoun = map[string]string{"issues": "issues", "pulls": "pull requests", "refs": "branches"}

func newReading(repository string, now time.Time, paged ...string) (*reading, error) {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("repository %q is not owner/name", repository)
	}
	read := &reading{activity: Activity{Repository: repository}, owner: owner, name: name, since: now.Add(-CollaborationWindow).UTC().Format(time.RFC3339), include: map[string]bool{"first": true}, cursors: map[string]string{}, changedFiles: map[int]int{}}
	for _, connection := range paged {
		read.include[connection] = true
	}
	return read, nil
}

func (r *reading) isPaging() bool {
	return r.include["issues"] || r.include["pulls"] || r.include["refs"]
}

func (r *reading) isDone() bool {
	return !r.include["first"] && !r.isPaging() && len(r.files) == 0
}

// next reads the reading's next page.
func (g GitHub) next(ctx context.Context, r *reading) error {
	if r.include["first"] || r.isPaging() {
		return g.nextRound(ctx, r)
	}
	return g.nextFiles(ctx, r)
}

// nextRound reads one round of the GraphQL query: everything the first time,
// then the next page of each connection that has one. After maxPageRounds
// rounds a connection's further pages are named unread.
func (g GitHub) nextRound(ctx context.Context, r *reading) error {
	response, err := g.query(ctx, r.owner, r.name, r.since, r.include, r.cursors)
	if err != nil && response.Data.Repository == nil {
		return err
	}
	activity := &r.activity
	if err != nil {
		activity.Unread = append(activity.Unread, err.Error())
	} else {
		for _, failure := range response.Errors {
			activity.Unread = append(activity.Unread, fmt.Sprintf("GitHub left part of %s unread: %s", activity.Repository, failure.Message))
		}
	}
	data := response.Data
	if r.include["first"] {
		if data.Viewer != nil {
			activity.Viewer = Actor{Login: data.Viewer.Login, Name: data.Viewer.Name}
		}
		readFirstRound(activity, response)
	}
	repo := data.Repository
	r.include["first"] = false
	missing := map[string]bool{"issues": repo.Issues == nil, "pulls": repo.PullRequests == nil, "refs": repo.Refs == nil}
	for _, connection := range connections {
		if r.include[connection] && missing[connection] {
			activity.Unread = append(activity.Unread, fmt.Sprintf("open %s on page %d: the connection was not read", connectionNoun[connection], r.rounds+1))
			r.include[connection] = false
		}
	}
	if r.include["issues"] {
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
		r.include["issues"], r.cursors["issuesAfter"] = repo.Issues.PageInfo.HasNextPage, repo.Issues.PageInfo.EndCursor
	}
	if r.include["pulls"] {
		for _, node := range repo.PullRequests.Nodes {
			pull := PullRequest{Number: node.Number, Title: node.Title, URL: node.URL, IsDraft: node.IsDraft, HeadRef: node.HeadRefName, FromFork: node.IsCrossRepository, CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt, Author: node.Author.actor(), Files: []string{}}
			for _, file := range node.Files.Nodes {
				pull.Files = append(pull.Files, file.Path)
			}
			r.changedFiles[node.Number] = node.ChangedFiles
			activity.PullRequests = append(activity.PullRequests, pull)
		}
		r.include["pulls"], r.cursors["pullsAfter"] = repo.PullRequests.PageInfo.HasNextPage, repo.PullRequests.PageInfo.EndCursor
	}
	if r.include["refs"] {
		for _, node := range repo.Refs.Nodes {
			activity.Branches = append(activity.Branches, Branch{Name: node.Name, Head: node.Target.Oid, CommittedAt: node.Target.CommittedDate, Author: node.Target.Author.actor()})
		}
		r.include["refs"], r.cursors["refsAfter"] = repo.Refs.PageInfo.HasNextPage, repo.Refs.PageInfo.EndCursor
	}
	r.rounds++
	if r.rounds == maxPageRounds {
		for _, connection := range connections {
			if r.include[connection] {
				activity.Unread = append(activity.Unread, fmt.Sprintf("open %s past the first %d pages", connectionNoun[connection], maxPageRounds))
				r.include[connection] = false
			}
		}
	}
	if !r.isPaging() {
		r.queueFiles()
	}
	return nil
}

// queueFiles lines up each pull request that changes more files than the
// GraphQL query listed, with the files it did list once each.
func (r *reading) queueFiles() {
	for i, pull := range r.activity.PullRequests {
		changed := r.changedFiles[pull.Number]
		if changed <= len(pull.Files) {
			continue
		}
		known := map[string]bool{}
		r.activity.PullRequests[i].Files = nil
		for _, file := range pull.Files {
			if !known[file] {
				r.activity.PullRequests[i].Files = append(r.activity.PullRequests[i].Files, file)
				known[file] = true
			}
		}
		r.files = append(r.files, pullFiles{index: i, last: min((changed+99)/100, maxPageRounds), known: known})
	}
}

// nextFiles reads the next page of the first pull request whose files are
// still being read, and names the files left unread once it reads its last.
func (g GitHub) nextFiles(ctx context.Context, r *reading) error {
	pending := &r.files[0]
	pull := &r.activity.PullRequests[pending.index]
	page := pending.pages + 1
	files, err := g.lines(ctx, "api", fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100&page=%d", r.activity.Repository, pull.Number, page), "--jq", ".[].filename")
	if err != nil {
		return err
	}
	pending.pages = page
	for _, file := range files {
		if !pending.known[file] {
			pull.Files = append(pull.Files, file)
			pending.known[file] = true
		}
	}
	changed := r.changedFiles[pull.Number]
	if page < pending.last && len(files) >= min(100, changed-(page-1)*100) {
		return nil
	}
	if len(pending.known) < changed {
		r.activity.Unread = append(r.activity.Unread, fmt.Sprintf("the changed files of pull request %d: read %d of %d files after the first %d pages", pull.Number, len(pending.known), changed, page))
	}
	r.files = r.files[1:]
	return nil
}

// giveUp names what the call that failed was reading as unread and moves
// past it: every connection it was paging, or the rest of the files of the
// pull request it was reading.
func (r *reading) giveUp(err error) {
	if r.isPaging() {
		for _, connection := range connections {
			if r.include[connection] {
				r.activity.Unread = append(r.activity.Unread, fmt.Sprintf("open %s after the first %d pages: %v", connectionNoun[connection], r.rounds, err))
				r.include[connection] = false
			}
		}
		r.queueFiles()
		return
	}
	pending := r.files[0]
	r.activity.Unread = append(r.activity.Unread, fmt.Sprintf("the changed files of pull request %d past the first %d: %v", r.activity.PullRequests[pending.index].Number, len(pending.known), err))
	r.files = r.files[1:]
}

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
// heads, comparing commit ids so no branch name needs escaping. A branch
// GitHub cannot compare, such as an orphan gh-pages, is named in Unread.
func (g GitHub) compareBranches(ctx context.Context, activity *Activity, now time.Time) {
	if activity.DefaultHead == "" {
		return
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
				activity.Unread = append(activity.Unread, fmt.Sprintf("the changed files of branch %s: %v", name, err))
				continue
			}
			branch.Files = files
		}
	}
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
	repo := response.Data.Repository
	hasRequiredConnections := repo != nil && (!include["issues"] || repo.Issues != nil) && (!include["pulls"] || repo.PullRequests != nil) && (!include["refs"] || repo.Refs != nil)
	hasReadableConnections := repo != nil && (include["issues"] && repo.Issues != nil || include["pulls"] && repo.PullRequests != nil || include["refs"] && repo.Refs != nil)
	if result.ExitCode != 0 && (decodeErr != nil || !hasReadableConnections || len(response.Errors) == 0) {
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
	if result.ExitCode != 0 {
		exitMessage := fmt.Sprintf("gh api graphql exited %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
		messages = append(messages, exitMessage)
	}
	if !hasRequiredConnections {
		if len(messages) == 0 {
			messages = []string{"no error given"}
		}
		err := fmt.Errorf("GitHub left part of %s/%s unread: %s", owner, name, strings.Join(messages, "; "))
		if !hasReadableConnections {
			return activityResponse{}, err
		}
		return response, err
	}
	if result.ExitCode != 0 {
		return response, errors.New(strings.Join(messages, "; "))
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
