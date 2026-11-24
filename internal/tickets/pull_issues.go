package tickets

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// pullIssuesBatchSize is how many pull requests one GraphQL query reads.
const pullIssuesBatchSize = 50

// pullAddress is a pull request's address on GitHub.
var pullAddress = regexp.MustCompile(`^https://github\.com/([\w.-]+/[\w.-]+)/pull/\d+$`)

// pullIssuesAnswer is one pull request as its aliased field answers. A
// resource that is not a pull request answers an empty object, so no URL.
type pullIssuesAnswer struct {
	URL     string `json:"url"`
	Body    string `json:"body"`
	Closing struct {
		Nodes []struct {
			Number     int `json:"number"`
			Repository struct {
				NameWithOwner string `json:"nameWithOwner"`
			} `json:"repository"`
		} `json:"nodes"`
	} `json:"closingIssuesReferences"`
}

// PullRequestIssues reads, for each pull request address, such as
// https://github.com/owner/name/pull/N, the issues it names: those GitHub
// links it to close and those BodyIssueRefs finds in its body, with the pull
// request's own repository standing for a bare #N. It reads them through
// GitHub's GraphQL API, fifty pull requests to a query, the addresses sent as
// query variables and never spliced into the query text. A duplicate address
// is read once. named has an entry, empty when it names none, for every pull
// request GitHub answered, keyed by the address as given. An address that is
// not a pull request address, or that GitHub answers with no pull request, is
// returned in unread instead of failing the whole read; a query that fails is
// an error.
func (g GitHub) PullRequestIssues(ctx context.Context, addresses []string) (named map[string][]IssueRef, unread []string, err error) {
	named = map[string][]IssueRef{}
	seen := map[string]bool{}
	var readable []string
	for _, address := range addresses {
		if seen[address] {
			continue
		}
		seen[address] = true
		if pullAddress.MatchString(address) {
			readable = append(readable, address)
		} else {
			unread = append(unread, address)
		}
	}
	for start := 0; start < len(readable); start += pullIssuesBatchSize {
		batch := readable[start:min(start+pullIssuesBatchSize, len(readable))]
		query, variables := pullIssuesQuery(batch)
		var answer map[string]*pullIssuesAnswer
		if err := g.API.GraphQL(ctx, query, variables, &answer); err != nil {
			return nil, nil, fmt.Errorf("read the issues %d pull requests name: %w", len(batch), err)
		}
		for index, address := range batch {
			pull := answer["pull"+strconv.Itoa(index)]
			if pull == nil || pull.URL == "" {
				unread = append(unread, address)
				continue
			}
			repository := pullAddress.FindStringSubmatch(address)[1]
			var refs []IssueRef
			for _, node := range pull.Closing.Nodes {
				if ref, ok := issueRefOf(node.Repository.NameWithOwner, strconv.Itoa(node.Number)); ok {
					refs = withIssueRef(refs, ref)
				}
			}
			for _, ref := range BodyIssueRefs(pull.Body, repository) {
				refs = withIssueRef(refs, ref)
			}
			named[address] = refs
		}
	}
	return named, unread, nil
}

// pullIssuesQuery is the query that reads each of addresses as an aliased
// field, pull0, pull1 and so on, and the variables that carry the addresses.
func pullIssuesQuery(addresses []string) (string, map[string]any) {
	variables := map[string]any{}
	var declarations, fields []string
	for index, address := range addresses {
		number := strconv.Itoa(index)
		variables["url"+number] = address
		declarations = append(declarations, "$url"+number+": URI!")
		fields = append(fields, "  pull"+number+": resource(url: $url"+number+") { ... on PullRequest { url body closingIssuesReferences(first: 50) { nodes { number repository { nameWithOwner } } } } }")
	}
	return "query(" + strings.Join(declarations, ", ") + ") {\n" + strings.Join(fields, "\n") + "\n}", variables
}
