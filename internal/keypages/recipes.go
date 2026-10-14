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
// to it may reach. A provider whose keys a fetch may read also names how its
// page's address carries the account, and each key it reads.
type Provider struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Hosts        []string `json:"hosts"`
	KeyPage      string   `json:"key_page"`
	SignedInPath string   `json:"signed_in_path"`
	// AccountPath matches the key page's path for one account, the account's
	// ID its one group: a fetch reads a key only on its login's account.
	AccountPath string `json:"account_path,omitempty"`
	Keys        []Key  `json:"keys,omitempty"`
}

// Key is one key a fetch reads from the key page, by the accessible names a
// person sees: the element that names the key's row, and the control that
// reveals a key the page masks, none for a key it shows.
type Key struct {
	Name   string `json:"name"`
	Row    string `json:"row"`
	Reveal string `json:"reveal,omitempty"`
	// Prefix is how every value of the key starts, so a masked or partial
	// read is never taken for it.
	Prefix string `json:"prefix"`
}

var (
	providerID  = regexp.MustCompile(`^[a-z0-9]{1,32}$`)
	hostPattern = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	keyName     = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	// dangerousControl names a control that changes keys rather than shows
	// them, which a recipe never clicks.
	dangerousControl = regexp.MustCompile(`(?i)\b(roll|delete|revoke|create)\b`)
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
		if err := checkKeys(provider); err != nil {
			return Recipes{}, fmt.Errorf("key page recipes: provider %s: %w", provider.ID, err)
		}
	}
	return recipes, nil
}

// checkKeys refuses keys a fetch could misread or a control it must never
// click: a name that is no credential name, a row or name listed twice, a
// control that rolls, deletes, revokes or creates a key, a live key, and
// keys with no account check.
func checkKeys(provider Provider) error {
	if len(provider.Keys) == 0 {
		return nil
	}
	account, err := regexp.Compile(provider.AccountPath)
	if err != nil || !strings.HasPrefix(provider.AccountPath, "^/") || !strings.HasSuffix(provider.AccountPath, "$") || account.NumSubexp() != 1 {
		return errors.New("its account path must be a valid pattern anchored with ^/ and $ with the account as its one group")
	}
	for index, key := range provider.Keys {
		switch {
		case !keyName.MatchString(key.Name):
			return fmt.Errorf("key %d's name %q is not a credential name", index+1, key.Name)
		case slices.IndexFunc(provider.Keys, func(other Key) bool { return other.Name == key.Name }) != index:
			return fmt.Errorf("key %s is listed twice", key.Name)
		case strings.TrimSpace(key.Row) == "":
			return fmt.Errorf("key %s names no row", key.Name)
		case slices.IndexFunc(provider.Keys, func(other Key) bool { return other.Row == key.Row }) != index:
			return fmt.Errorf("key %s's row %q is another key's too", key.Name, key.Row)
		case dangerousControl.MatchString(key.Row) || dangerousControl.MatchString(key.Reveal):
			return fmt.Errorf("key %s names a control that rolls, deletes, revokes or creates keys", key.Name)
		case strings.TrimSpace(key.Prefix) == "":
			return fmt.Errorf("key %s names no prefix", key.Name)
		case strings.Contains(strings.ToLower(key.Prefix), "live") || slices.Contains(strings.Split(key.Name, "_"), "LIVE"):
			return fmt.Errorf("key %s is a live key, which a recipe never reads", key.Name)
		}
	}
	return nil
}

// Fetch is the provider whose reviewed key page page is, and its keys among
// names, in their order: page must be the recipe's key page as written, and
// names must hold one or more of the keys it reads, and no name twice. A
// name it does not read is left for a person to paste.
func (r Recipes) Fetch(page string, names []string) (Provider, []Key, error) {
	index := slices.IndexFunc(r.Providers, func(provider Provider) bool { return provider.KeyPage != "" && provider.KeyPage == page })
	if index < 0 {
		return Provider{}, nil, fmt.Errorf("no reviewed recipe reads keys from %s", page)
	}
	provider := r.Providers[index]
	var keys []Key
	for at, name := range names {
		if slices.Index(names, name) != at {
			return Provider{}, nil, fmt.Errorf("%s is named twice", name)
		}
		if key := slices.IndexFunc(provider.Keys, func(key Key) bool { return key.Name == name }); key >= 0 {
			keys = append(keys, provider.Keys[key])
		}
	}
	if len(keys) == 0 {
		return Provider{}, nil, fmt.Errorf("the %s recipe reads none of the keys asked for", provider.Name)
	}
	return provider, keys, nil
}

// AccountPage is the key page of account: its path is the key page's under
// the account, and the whole of it must match the account path with account
// as its group, so an ID that is no account or reaches past it is refused.
func (p Provider) AccountPage(account string) (string, error) {
	pattern, err := regexp.Compile(p.AccountPath)
	if err != nil || p.AccountPath == "" {
		return "", fmt.Errorf("the %s recipe has no account path", p.Name)
	}
	page, err := url.Parse(p.KeyPage)
	if err != nil {
		return "", err
	}
	path := "/" + account + page.Path
	if match := pattern.FindStringSubmatch(path); match == nil || match[1] != account {
		return "", fmt.Errorf("%q is not a %s account", account, p.Name)
	}
	page.Path = path
	return page.String(), nil
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
