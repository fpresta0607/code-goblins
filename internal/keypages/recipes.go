// Package keypages reads the fleet's key page recipes, config/key-pages.json,
// which the binary carries as built: what a provider's dashboard is, so a
// browser signed in to it reaches nothing else. A recipe runs only as it was
// reviewed and merged, never from a file a goblin could write.
package keypages

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Recipes are every provider the fleet has a reviewed recipe for.
type Recipes struct {
	Providers []Provider `json:"providers"`
}

// Provider is one provider's dashboard: its ID, which logins and requests
// name it by, the name the Overlord reads, and the hosts a browser signed in
// to it may reach.
type Provider struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Hosts        []string `json:"hosts"`
	KeyPage      string   `json:"key_page"`
	SignedInPath string   `json:"signed_in_path"`
}

var (
	providerID  = regexp.MustCompile(`^[a-z0-9]{1,32}$`)
	hostPattern = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
)

// Parse reads recipes and refuses any field it does not know, a provider
// with no hosts, and a host that is not a name or a wildcard over one.
func Parse(data []byte) (Recipes, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var recipes Recipes
	if err := decoder.Decode(&recipes); err != nil {
		return Recipes{}, fmt.Errorf("key page recipes: %w", err)
	}
	for index, provider := range recipes.Providers {
		switch {
		case !providerID.MatchString(provider.ID):
			return Recipes{}, fmt.Errorf("key page recipes: provider %d's id is 1 to 32 lowercase letters and digits", index+1)
		case slices.IndexFunc(recipes.Providers, func(other Provider) bool { return other.ID == provider.ID }) != index:
			return Recipes{}, fmt.Errorf("key page recipes: provider %s is listed twice", provider.ID)
		case strings.TrimSpace(provider.Name) == "":
			return Recipes{}, fmt.Errorf("key page recipes: provider %s has no name", provider.ID)
		case len(provider.Hosts) == 0:
			return Recipes{}, fmt.Errorf("key page recipes: provider %s names no hosts", provider.ID)
		}
		for _, host := range provider.Hosts {
			if !hostPattern.MatchString(host) {
				return Recipes{}, fmt.Errorf("key page recipes: provider %s's host %q is not a host name or *. and one", provider.ID, host)
			}
		}
		page, err := url.Parse(provider.KeyPage)
		if err != nil || page.Scheme != "https" || page.User != nil || page.RawQuery != "" || page.Fragment != "" || page.Port() != "" || !slices.ContainsFunc(provider.Hosts, func(host string) bool {
			return page.Hostname() == host || strings.HasPrefix(host, "*.") && strings.HasSuffix(page.Hostname(), host[1:])
		}) {
			return Recipes{}, fmt.Errorf("key page recipes: provider %s's sign-in page must use HTTPS on its reviewed hosts, without credentials, a port, a query or a fragment", provider.ID)
		}
		if _, err := regexp.Compile(provider.SignedInPath); err != nil || !strings.HasPrefix(provider.SignedInPath, "^/") || !strings.HasSuffix(provider.SignedInPath, "$") {
			return Recipes{}, fmt.Errorf("key page recipes: provider %s's sign-in path must be a valid pattern anchored with ^/ and $", provider.ID)
		}
	}
	return recipes, nil
}

// Provider is the recipe for id, or an error naming the providers there are.
func (r Recipes) Provider(id string) (Provider, error) {
	index := slices.IndexFunc(r.Providers, func(provider Provider) bool { return provider.ID == id })
	if index < 0 {
		known := make([]string, 0, len(r.Providers))
		for _, provider := range r.Providers {
			known = append(known, provider.ID)
		}
		if len(known) == 0 {
			return Provider{}, errors.New("no provider has a key page recipe")
		}
		return Provider{}, fmt.Errorf("no key page recipe for %q: the recipes are for %s", id, strings.Join(known, ", "))
	}
	return r.Providers[index], nil
}
