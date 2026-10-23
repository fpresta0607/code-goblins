package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// RootLabel is on every issue filed for a root issue the fleet met while it
// ran: a cause outside the task that found it, to be fixed where it starts.
const RootLabel = "ops-root"

const (
	rootLabelColor       = "B60205"
	rootLabelDescription = "A root issue found while the fleet runs, fixed at its cause"
)

// rootMarker ends the body of every issue the fleet files for a root, naming
// the root a recurrence is matched by. It is an HTML comment, which GitHub
// does not show.
var rootMarker = regexp.MustCompile(`<!-- ops-root: (.+?) -->`)

// Root is a root issue to file: its title, what happens and why, the key a
// recurrence is matched by, and the evidence of this sighting.
type Root struct {
	Title string
	Body  string
	// Key names the cause, such as a test's package and name or a workflow
	// and job, so whoever meets it again finds the same issue. Without one
	// the title is the key.
	Key      string
	Evidence string
}

// RootKey is the key root is matched by: its Key or else its title, in
// lower case with its spaces collapsed and without angle brackets, which
// would end the marker that carries it.
func (r Root) RootKey() string {
	key := r.Key
	if strings.TrimSpace(key) == "" {
		key = r.Title
	}
	key = strings.NewReplacer("<", "", ">", "").Replace(key)
	return strings.ToLower(strings.Join(strings.Fields(key), " "))
}

// FiledRoot is where a root was filed: a new issue, or a comment on the open
// issue that already had it.
type FiledRoot struct {
	Number       int
	URL          string
	IsRecurrence bool
}

// OpenRoot is an open issue labelled RootLabel.
type OpenRoot struct {
	Number int
	Title  string
	Body   string
	URL    string
	// Key is the root its marker names, or "" for an issue filed by hand.
	Key       string
	Assignees []string
	Labels    []string
	// Association is how its author stands to the repository, as GitHub
	// says: OWNER, MEMBER, COLLABORATOR, CONTRIBUTOR, NONE and the like.
	Association string
}

// matches keeps an explicit test key authoritative, but also recognizes a
// manual report whose title names the test and whose text pairs it with one
// package. Merely mentioning a package elsewhere does not identify the test.
func (o OpenRoot) matches(root Root, repository string) bool {
	if o.Key != "" && o.Key == root.RootKey() {
		return true
	}
	if o.Key == "" && strings.EqualFold(strings.Join(strings.Fields(o.Title), " "), strings.Join(strings.Fields(root.Title), " ")) {
		return true
	}
	if strings.HasPrefix(o.Key, "test:") {
		return false
	}
	test, isTest := strings.CutPrefix(root.RootKey(), "test:")
	identity := strings.Fields(test)
	if !isTest || len(identity) != 2 || !slices.Contains(rootIdentifiers(o.Title), identity[1]) {
		return false
	}
	packagePath, isRepositoryPackage := strings.CutPrefix(identity[0], "github.com/"+strings.ToLower(repository)+"/")
	words := rootIdentifiers(o.Title + "\n" + o.Body)
	hasMatch := false
	for index, word := range words {
		if word != identity[1] {
			continue
		}
		var packages []string
		if index > 0 && strings.Contains(words[index-1], "/") {
			packages = append(packages, words[index-1])
		}
		if index+1 < len(words) && strings.Contains(words[index+1], "/") {
			packages = append(packages, words[index+1])
		}
		if index+2 < len(words) && words[index+1] == "in" && strings.Contains(words[index+2], "/") {
			packages = append(packages, words[index+2])
		}
		for _, named := range packages {
			if named != identity[0] && !(isRepositoryPackage && named == packagePath) {
				return false
			}
			hasMatch = true
		}
	}
	return hasMatch
}

func rootIdentifiers(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_./-", r)
	})
	for index, word := range words {
		words[index] = strings.TrimSuffix(word, ".")
	}
	return words
}

// FileRoot files root on repository, owner/name: it reads the open issues
// labelled RootLabel first, and when one has the same root it comments on it
// with the date and the new evidence, else it opens an issue with the label,
// creating the label the first time. A read that fails files nothing, so a
// root is never filed twice because its issue could not be seen.
func (g GitHub) FileRoot(ctx context.Context, repository string, root Root, now time.Time) (FiledRoot, error) {
	if strings.TrimSpace(root.Title) == "" {
		return FiledRoot{}, errors.New("a root issue needs a title")
	}
	open, err := g.OpenRoots(ctx, repository)
	if err != nil {
		return FiledRoot{}, err
	}
	for _, issue := range open {
		if !issue.matches(root, repository) {
			continue
		}
		seen := strings.TrimSpace(root.Evidence)
		if seen == "" {
			seen = strings.TrimSpace(root.Body)
		}
		comment := "Seen again on " + now.UTC().Format("2006-01-02 15:04Z") + "."
		if seen != "" {
			comment += "\n\n" + seen
		}
		if _, err := g.api(ctx, "POST", fmt.Sprintf("repos/%s/issues/%d/comments", repository, issue.Number), "body="+comment); err != nil {
			return FiledRoot{}, err
		}
		return FiledRoot{Number: issue.Number, URL: issue.URL, IsRecurrence: true}, nil
	}
	_, err = g.api(ctx, "POST", "repos/"+repository+"/labels", "name="+RootLabel, "color="+rootLabelColor, "description="+rootLabelDescription)
	if err != nil && !isExistingLabel(err) {
		return FiledRoot{}, err
	}
	body := strings.TrimSpace(root.Body)
	if evidence := strings.TrimSpace(root.Evidence); evidence != "" {
		body += "\n\n## Evidence\n\n" + now.UTC().Format("2006-01-02 15:04Z") + ": " + evidence
	}
	body = strings.TrimSpace(body) + "\n\n<!-- ops-root: " + root.RootKey() + " -->\n"
	out, err := g.api(ctx, "POST", "repos/"+repository+"/issues", "title="+strings.TrimSpace(root.Title), "body="+body, "labels[]="+RootLabel)
	if err != nil {
		return FiledRoot{}, err
	}
	var opened githubIssue
	if err := json.Unmarshal(out, &opened); err != nil || opened.Number == 0 {
		return FiledRoot{}, fmt.Errorf("read the issue GitHub opened in %s: %v", repository, err)
	}
	return FiledRoot{Number: opened.Number, URL: opened.URL}, nil
}

// OpenRoots reads every open issue labelled RootLabel on repository, a page
// of a hundred at a time, up to maxPageRounds pages; a repository with more
// is an error, never a partial answer, since an issue left unread would be
// filed again.
func (g GitHub) OpenRoots(ctx context.Context, repository string) ([]OpenRoot, error) {
	var open []OpenRoot
	for page := 1; page <= maxPageRounds; page++ {
		out, err := g.api(ctx, "GET", fmt.Sprintf("repos/%s/issues?labels=%s&state=open&per_page=100&page=%d", repository, RootLabel, page))
		if err != nil {
			return nil, err
		}
		var issues []struct {
			githubIssue
			Title       string `json:"title"`
			Body        string `json:"body"`
			Association string `json:"author_association"`
		}
		if err := json.Unmarshal(out, &issues); err != nil {
			return nil, fmt.Errorf("read the open %s issues of %s: %w", RootLabel, repository, err)
		}
		for _, issue := range issues {
			if issue.PullRequest != nil {
				continue
			}
			root := OpenRoot{Number: issue.Number, Title: issue.Title, Body: issue.Body, URL: issue.URL, Association: issue.Association}
			if marker := rootMarker.FindStringSubmatch(issue.Body); marker != nil {
				root.Key = marker[1]
			}
			for _, assignee := range issue.Assignees {
				root.Assignees = append(root.Assignees, assignee.Login)
			}
			for _, label := range issue.Labels {
				root.Labels = append(root.Labels, label.Name)
			}
			open = append(open, root)
		}
		if len(issues) < 100 {
			return open, nil
		}
	}
	return nil, fmt.Errorf("%s has more than %d open %s issues, so they cannot all be read", repository, maxPageRounds*100, RootLabel)
}

// Repository is what GitHub says of a repository: the account or
// organization that owns it, whether anyone can see it, and whether it
// takes issues.
type Repository struct {
	Owner     string
	IsPublic  bool
	HasIssues bool
}

// ReadRepository reads what GitHub says of repository, owner/name.
func (g GitHub) ReadRepository(ctx context.Context, repository string) (Repository, error) {
	out, err := g.api(ctx, "GET", "repos/"+repository)
	if err != nil {
		return Repository{}, err
	}
	var answer struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Private   *bool `json:"private"`
		HasIssues *bool `json:"has_issues"`
	}
	if err := json.Unmarshal(out, &answer); err != nil || answer.Owner.Login == "" || answer.Private == nil || answer.HasIssues == nil {
		return Repository{}, fmt.Errorf("GitHub did not say whose %s is, whether it is private and whether it takes issues: %v", repository, err)
	}
	return Repository{Owner: answer.Owner.Login, IsPublic: !*answer.Private, HasIssues: *answer.HasIssues}, nil
}
