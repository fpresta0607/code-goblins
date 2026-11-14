package githubapi

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// repositorySlug is a GitHub repository's owner/name.
var repositorySlug = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

// SplitRemote reads the host and the repository path of a Git remote in any
// of the forms git takes: a URL with a scheme, and the scp-like user@host:path
// form. A remote kept on this machine, as an absolute or relative path or a
// file URL, names no host.
func SplitRemote(remote string) (host, repository string, ok bool) {
	if remote == "" || filepath.IsAbs(remote) || path.IsAbs(remote) || strings.HasPrefix(remote, ".") {
		return "", "", false
	}
	if _, rest, hasScheme := strings.Cut(remote, "://"); hasScheme {
		return splitAuthority(rest)
	}
	if scheme, rest, found := strings.Cut(remote, ":/"); found && isSchemeName(scheme) {
		return splitAuthority(rest)
	}
	hostAndPath := remote
	if _, after, found := strings.Cut(hostAndPath, "@"); found {
		hostAndPath = after
	}
	host, repository, found := strings.Cut(hostAndPath, ":")
	if !found || strings.ContainsAny(host, `/\`) {
		return "", "", false
	}
	return host, repository, host != ""
}

// splitAuthority reads the host and the repository path of a remote from its
// authority and path, whichever scheme carried them.
func splitAuthority(rest string) (host, repository string, ok bool) {
	authority, after, _ := strings.Cut(rest, "/")
	if _, userinfo, found := strings.Cut(authority, "@"); found {
		authority = userinfo
	}
	host, _, _ = strings.Cut(authority, ":")
	return host, after, host != ""
}

// isSchemeName reports whether name is a URL scheme: a letter followed by
// letters, digits, '+', '-' or '.'.
func isSchemeName(name string) bool {
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return name != ""
}

// IsGitHubHost reports whether host is one GitHub serves. The fleet reads a
// repository's pull requests, tickets, trains and task evidence on github.com
// only, so a checkout kept elsewhere is hosted somewhere the fleet does not
// read and is asked of GitHub nothing.
func IsGitHubHost(host string) bool {
	return strings.EqualFold(host, "github.com") || strings.EqualFold(host, "ssh.github.com")
}

// RemoteRepository reads the owner/name of the GitHub repository remote is,
// in each form SplitRemote reads on each host IsGitHubHost serves. It is the
// one reading of a GitHub remote, so a checkout the CI poll reads as GitHub's
// is read as the same repository wherever else its origin is read (#1485,
// #1697).
func RemoteRepository(remote string) (slug string, ok bool) {
	host, repository, hasRemote := SplitRemote(remote)
	if !hasRemote || !IsGitHubHost(host) {
		return "", false
	}
	slug = strings.TrimSuffix(strings.Trim(repository, "/"), ".git")
	return slug, repositorySlug.MatchString(slug)
}
