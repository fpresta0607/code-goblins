package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/terminal"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const (
	// credentialLifetime is how long a request waits for the Overlord's save.
	credentialLifetime = 24 * time.Hour
	// closedCredentialRetention is how long a saved or expired request stays
	// listed.
	closedCredentialRetention = 7 * 24 * time.Hour
	// maxCredentialRequests bounds the list; an open request is never dropped
	// to make room.
	maxCredentialRequests = 64
	// maxCredentialNames is the most names one request asks for.
	maxCredentialNames = 10
	// maxCredentialValue bounds one value and maxCredentialBody one save.
	maxCredentialValue = 16 << 10
	maxCredentialBody  = 64 << 10
	// credentialRefreshTimeout bounds the refresh a save starts.
	credentialRefreshTimeout = 2 * time.Minute
	// credentialInbox is where a goblin's request waits for the supervisor.
	credentialInbox = "credential-requests-inbox"
)

// credentialID is the ID cfo auth request gives a request.
var credentialID = regexp.MustCompile(`^cred-[0-9a-f]{16}$`)

// CredentialRequest asks the Overlord for credential values by name. It never
// holds a value: he pastes each one on the request's card on the board, and
// the board stores it straight into the project's scope of the credential
// store, the way cfo auth store does. A request takes one save and expires
// after a day.
type CredentialRequest struct {
	ID string `json:"id"`
	// Generation is minted when the board takes the request, and a save must
	// name it, so a card for an earlier request never saves into this one.
	Generation string `json:"generation"`
	// Identity is the asker's: the registered CFO's, or the goblin's task
	// generation and terminal, and By says which of the two asked.
	Identity string `json:"identity"`
	By       string `json:"by"`
	// Task is the goblin that needs the values: the asker, or the goblin the
	// CFO asked for.
	Task    string   `json:"task,omitempty"`
	Project string   `json:"project"`
	Names   []string `json:"names"`
	Why     string   `json:"why"`
	Link    string   `json:"link,omitempty"`
	// Existing are the names the scope already held a value for when the
	// board last looked, which a save replaces only once confirmed.
	Existing []string         `json:"existing,omitempty"`
	Hints    []CredentialHint `json:"hints,omitempty"`
	// State is open, saved or expired.
	State    string   `json:"state"`
	Saved    []string `json:"saved,omitempty"`
	Replaced []string `json:"replaced,omitempty"`
	// Told are the running goblins told to re-source their credentials.
	Told      []string   `json:"told,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

// CredentialHint is a name's format hint from its project's auth.json: how
// its value is expected to start, and advice for values that start a
// certain way. The card warns with it and never refuses a value for it.
type CredentialHint struct {
	Name     string               `json:"name"`
	Prefixes []string             `json:"prefixes,omitempty"`
	Warn     []auth.FormatWarning `json:"warn,omitempty"`
}

func (r CredentialRequest) clone() CredentialRequest {
	r.Names, r.Existing, r.Hints = slices.Clone(r.Names), slices.Clone(r.Existing), slices.Clone(r.Hints)
	r.Saved, r.Replaced, r.Told = slices.Clone(r.Saved), slices.Clone(r.Replaced), slices.Clone(r.Told)
	if r.ClosedAt != nil {
		closed := *r.ClosedAt
		r.ClosedAt = &closed
	}
	return r
}

// sameCredentialRequest reports whether two filings ask the same thing, so a
// retry of one changes nothing.
func sameCredentialRequest(a, b CredentialRequest) bool {
	return a.Identity == b.Identity && a.By == b.By && a.Task == b.Task && a.Project == b.Project && slices.Equal(a.Names, b.Names) && a.Why == b.Why && a.Link == b.Link
}

// validCredentialRequest refuses anything but names, a reason and a page to
// get them from. A name, a reason or a link shaped like a credential value is
// refused, and no refusal repeats what it refused.
func validCredentialRequest(r CredentialRequest) error {
	switch {
	case !credentialID.MatchString(r.ID):
		return errors.New("a credential request needs an ID of cred- and 16 hexadecimal digits")
	case len(r.Identity) != 64:
		return errors.New("a credential request needs its asker's identity")
	case r.By != "cfo" && r.By != "goblin":
		return errors.New("a credential request is asked by the CFO or a goblin")
	case r.By == "goblin" && r.Task == "":
		return errors.New("a goblin's credential request names its task")
	case r.Task != "" && state.ValidTaskID(r.Task) != nil:
		return errors.New("a credential request's task is not a task ID")
	case !auth.ValidProjectName(r.Project):
		return errors.New("a credential request needs a project scope")
	case len(r.Names) == 0 || len(r.Names) > maxCredentialNames:
		return fmt.Errorf("a credential request asks for 1 to %d names", maxCredentialNames)
	}
	for index, name := range r.Names {
		if shape := auth.SecretShape(name); shape != "" {
			return fmt.Errorf("name %d looks like a credential value, not a name: %s; a request takes names only", index+1, shape)
		}
		if !auth.ValidEnvName(name) {
			return fmt.Errorf("name %d is not an environment variable name", index+1)
		}
		if slices.Index(r.Names, name) != index {
			return fmt.Errorf("name %d is asked for twice", index+1)
		}
	}
	why := strings.TrimSpace(r.Why)
	if why == "" || len(r.Why) > 300 || strings.ContainsFunc(r.Why, unicode.IsControl) {
		return errors.New("a credential request says what the values are for in one line of at most 300 characters")
	}
	for index, word := range strings.Fields(why) {
		if shape := auth.SecretShape(word); shape != "" {
			return fmt.Errorf("word %d of the reason looks like a credential value: %s", index+1, shape)
		}
	}
	if r.Link != "" {
		return validCredentialLink(r.Link)
	}
	return nil
}

// validCredentialLink takes a plain https page: no credentials, query or
// fragment, and nothing in its path shaped like a value.
func validCredentialLink(link string) error {
	u, err := url.Parse(link)
	if err != nil || len(link) > 500 || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(link, "?") || strings.HasSuffix(link, "#") {
		return errors.New("a credential request's link is an https page of at most 500 characters with no credentials, query or fragment")
	}
	for index, segment := range strings.Split(u.Path, "/") {
		if shape := auth.SecretShape(segment); shape != "" {
			return fmt.Errorf("part %d of the link's path looks like a credential value: %s", index, shape)
		}
	}
	return nil
}

// FileCredentialRequest files r from the asker's own process: a goblin for
// its own task, proven from inside its terminal as cfo notify proves it, goes
// through the inbox the supervisor reads; anyone else must be the registered
// CFO, whom the supervisor proves over its pipe. It returns the request as
// filed, with its ID.
func FileCredentialRequest(ctx context.Context, h home.Home, terminals terminal.Opener, r CredentialRequest) (CredentialRequest, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return r, err
	}
	r.ID = "cred-" + hex.EncodeToString(random[:])
	if r.Task != "" {
		if meta, goblinErr := goblinAsker(ctx, h.State, terminals, r.Task); goblinErr == nil {
			r.Identity, r.By = goblinIdentity(meta), "goblin"
			return r, spoolCredentialRequest(h.State, r)
		} else if !RunsUnderRegisteredCFO(h.State) {
			return r, fmt.Errorf("only the goblin of %s, from its own terminal, or the registered CFO can ask for credentials for it: %w", r.Task, goblinErr)
		}
	}
	identity, release, err := (&CFOConnection{State: h.State, Terminals: terminals}).CallerIdentity(ctx)
	if err != nil {
		return r, fmt.Errorf("only the registered CFO, or a goblin naming its own task with --task, can ask for credentials: %w", err)
	}
	defer release()
	r.Identity, r.By = identity, "cfo"
	if err := validCredentialRequest(r); err != nil {
		return r, err
	}
	return r, sendPipeRequest(h.State, runPipeRequest{Kind: "credential", Credential: &r})
}

func spoolCredentialRequest(stateDir string, r CredentialRequest) error {
	if err := validCredentialRequest(r); err != nil {
		return err
	}
	dir := filepath.Join(stateDir, credentialInbox)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if entries, err := os.ReadDir(dir); err != nil {
		return err
	} else if len(entries) >= maxCredentialRequests {
		return errors.New("the credential request inbox is full")
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(r.ID))
	return fsx.AtomicWriteFile(filepath.Join(dir, hex.EncodeToString(sum[:])+".json"), data)
}

// ingestCredentialRequests takes each goblin's request from the inbox once
// its task is still the generation that asked. A request in the inbox that
// speaks for the CFO is refused: the CFO's come only over the pipe.
func (s *Service) ingestCredentialRequests() error {
	dir := filepath.Join(s.Store.Home.State, credentialInbox)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries[:min(len(entries), maxCredentialRequests)] {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var r CredentialRequest
		var invalid error
		if info, err := entry.Info(); err != nil {
			return err
		} else if info.Size() > 16<<10 {
			invalid = errors.New("the request exceeds its size limit")
		} else if data, err := os.ReadFile(path); err != nil {
			return err
		} else if json.Unmarshal(data, &r) != nil {
			invalid = errors.New("the request is not valid JSON")
		}
		if invalid == nil && r.By != "goblin" {
			invalid = errFromInbox
		}
		if invalid == nil {
			if meta, err := state.ReadTaskMeta(s.Store.Home.State, r.Task); err != nil || goblinIdentity(meta) != r.Identity {
				invalid = errors.New("the goblin that asked restarted or ended")
			}
		}
		if invalid == nil {
			_, invalid = s.acceptCredentialRequest(r)
		}
		if errors.Is(invalid, ErrStorage) {
			return invalid
		}
		if invalid != nil {
			s.Store.mu.Lock()
			s.Store.issue("Credential request refused: " + bounded(invalid.Error(), 300))
			err := s.Store.save()
			s.Store.mu.Unlock()
			if err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

// acceptCredentialRequest puts a proven asker's request on the board under a
// fresh generation, with the names its scope already holds and each name's
// format hint, open for a day.
func (s *Service) acceptCredentialRequest(r CredentialRequest) (CredentialRequest, error) {
	r.Existing, r.Hints, r.Saved, r.Replaced, r.Told, r.Reason, r.ClosedAt = nil, nil, nil, nil, nil, "", nil
	if err := validCredentialRequest(r); err != nil {
		return r, err
	}
	var generation [16]byte
	if _, err := rand.Read(generation[:]); err != nil {
		return r, err
	}
	r.Generation = hex.EncodeToString(generation[:])
	if s.Options.Credentials != nil {
		if store, err := s.Options.Credentials(); err == nil {
			if existing, err := storedNames(store, r.Project, r.Names); err == nil {
				r.Existing = existing
			}
		}
	}
	if manifest, err := auth.LoadManifest(s.Store.Home.Data, r.Project); err == nil {
		for _, name := range r.Names {
			if format, found := manifest.FormatFor(name); found {
				r.Hints = append(r.Hints, CredentialHint{Name: name, Prefixes: format.Prefixes, Warn: format.Warn})
			}
		}
	}
	now := time.Now().UTC()
	r.State, r.CreatedAt, r.ExpiresAt = "open", now, now.Add(credentialLifetime)
	return s.Store.acceptCredential(r)
}

// storedNames are the names a scope already holds a value for, read from the
// store's listing, which never returns a value.
func storedNames(store auth.Store, project string, names []string) ([]string, error) {
	keys, err := store.Keys()
	if err != nil {
		return nil, err
	}
	var stored []string
	for _, name := range names {
		if slices.Contains(keys, auth.Key{Project: project, Name: name}) {
			stored = append(stored, name)
		}
	}
	return stored, nil
}

func (s *Store) acceptCredential(r CredentialRequest) (CredentialRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := slices.IndexFunc(s.db.Credentials, func(prior CredentialRequest) bool { return prior.ID == r.ID }); i >= 0 {
		if !sameCredentialRequest(s.db.Credentials[i], r) {
			return r, errors.New("credential request ID already used")
		}
		return s.db.Credentials[i].clone(), nil
	}
	if i := slices.IndexFunc(s.db.Credentials, func(prior CredentialRequest) bool {
		return prior.State == "open" && prior.Identity == r.Identity && prior.Project == r.Project && slices.Equal(prior.Names, r.Names)
	}); i >= 0 {
		return r, fmt.Errorf("a request for these names in %s is already open: %s", r.Project, s.db.Credentials[i].ID)
	}
	if len(s.db.Credentials) >= maxCredentialRequests {
		closed := slices.IndexFunc(s.db.Credentials, func(old CredentialRequest) bool { return old.State != "open" })
		if closed < 0 {
			return r, fmt.Errorf("%w: the board already holds %d open credential requests", ErrDeferred, maxCredentialRequests)
		}
		s.db.Credentials = slices.Delete(s.db.Credentials, closed, closed+1)
	}
	s.db.Credentials = append(s.db.Credentials, r.clone())
	return r, s.save()
}

func (s *Store) credential(id string) (CredentialRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Credentials, func(r CredentialRequest) bool { return r.ID == id })
	if i < 0 {
		return CredentialRequest{}, false
	}
	return s.db.Credentials[i].clone(), true
}

// updateCredential changes one request under the store lock and saves it.
func (s *Store) updateCredential(id string, change func(*CredentialRequest)) (CredentialRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Credentials, func(r CredentialRequest) bool { return r.ID == id })
	if i < 0 {
		return CredentialRequest{}, fmt.Errorf("credential request %s is gone", id)
	}
	change(&s.db.Credentials[i])
	return s.db.Credentials[i].clone(), s.save()
}

// credentialSave is what a request's card posts: the request it answers, a
// value for each name the Overlord filled, and the names he confirmed
// replacing. It is the one body the board takes a secret value in.
type credentialSave struct {
	ID         string            `json:"id"`
	Generation string            `json:"generation"`
	Values     map[string]string `json:"values"`
	Replace    []string          `json:"replace"`
}

// credentialOutcome is a save's answer: names and states, never a value.
type credentialOutcome struct {
	ID       string   `json:"id"`
	State    string   `json:"state"`
	Saved    []string `json:"saved"`
	Replaced []string `json:"replaced"`
}

// credentialRefusal is a save the board did not take, with the status the
// card reads and, for names already stored, the names to confirm replacing.
type credentialRefusal struct {
	status   int
	Message  string   `json:"error"`
	Existing []string `json:"existing,omitempty"`
}

func (r credentialRefusal) Error() string { return r.Message }

// proxyHeader reports a header a proxy adds on the way to the board, such as
// Tailscale serve's forwarding and identity headers.
func proxyHeader(name string) bool {
	name = http.CanonicalHeaderKey(name)
	return name == "Forwarded" || name == "Via" || name == "X-Real-Ip" || strings.HasPrefix(name, "X-Forwarded-") || strings.HasPrefix(name, "Tailscale-")
}

// loopbackProblem refuses a save that did not come from the board's own page
// on this machine: its Host names 127.0.0.1, localhost or ::1 with the
// board's port, its peer is this machine, and no proxy handled it. A board
// shared through Tailscale serve reaches the same port on this machine, so the
// proxy's headers are what tell it apart.
func loopbackProblem(r *http.Request, board string) string {
	refusal := "The board takes values only from its own page on this PC, at 127.0.0.1; use the terminal command on the card."
	host, port, err := net.SplitHostPort(r.Host)
	_, boardPort, boardErr := net.SplitHostPort(board)
	if err != nil || boardErr != nil || port != boardPort || host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return refusal
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(peer); err != nil || ip == nil || !ip.IsLoopback() {
		return refusal
	}
	for name := range r.Header {
		if proxyHeader(name) {
			return "This save came through a proxy, and the board takes values only from its own page on this PC; use the terminal command on the card."
		}
	}
	return ""
}

// saveCredentials takes a request card's values. Nothing about the body is
// ever logged or repeated: a refusal names names only, and a panic answers
// with a fixed message and logs nothing.
func (h *HTTP) saveCredentials(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if recover() != nil {
			apiError(w, http.StatusInternalServerError, "The credentials could not be saved.")
		}
	}()
	if problem := loopbackProblem(r, h.Host); problem != "" {
		apiError(w, http.StatusForbidden, problem)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		apiError(w, http.StatusUnsupportedMediaType, "JSON required")
		return
	}
	var input credentialSave
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCredentialBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
		apiError(w, http.StatusBadRequest, "Send one save: the request's ID and generation, and a value for each name.")
		return
	}
	outcome, err := h.Service.saveCredentials(input)
	var refusal credentialRefusal
	switch {
	case errors.As(err, &refusal):
		respond(w, refusal.status, refusal)
	case err != nil:
		apiError(w, http.StatusInternalServerError, err.Error())
	default:
		respond(w, http.StatusOK, outcome)
	}
}

// saveCredentials stores one save's values into its request's project scope
// through the store cfo auth store writes, closes the request so it takes no
// other save, and then, after answering, refreshes the project's running
// goblins and tells the CFO the names.
func (s *Service) saveCredentials(input credentialSave) (credentialOutcome, error) {
	refuse := func(status int, message string) (credentialOutcome, error) {
		return credentialOutcome{}, credentialRefusal{status: status, Message: message}
	}
	if s.Options.Credentials == nil {
		return refuse(http.StatusServiceUnavailable, "This board cannot store credentials; use the terminal command on the card.")
	}
	s.credentialSaves.Lock()
	defer s.credentialSaves.Unlock()
	request, found := s.Store.credential(input.ID)
	switch {
	case !found || input.Generation == "" || input.Generation != request.Generation:
		return refuse(http.StatusConflict, "This credential request is not on the board; refresh the board.")
	case request.State != "open":
		return refuse(http.StatusConflict, "This credential request was already "+request.State+"; ask for a new one to store again.")
	case !time.Now().Before(request.ExpiresAt):
		return refuse(http.StatusConflict, "This credential request expired; ask for a new one.")
	case len(input.Values) == 0:
		return refuse(http.StatusBadRequest, "Paste at least one value.")
	}
	names := make([]string, 0, len(input.Values))
	for name := range input.Values {
		if !slices.Contains(request.Names, name) {
			return refuse(http.StatusBadRequest, "The save names something this request does not ask for; refresh the board.")
		}
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		value := input.Values[name]
		switch {
		case strings.TrimSpace(value) == "":
			return refuse(http.StatusBadRequest, name+" is empty.")
		case len(value) > maxCredentialValue:
			return refuse(http.StatusBadRequest, fmt.Sprintf("%s is longer than %d KiB.", name, maxCredentialValue>>10))
		case strings.ContainsFunc(value, unicode.IsControl):
			return refuse(http.StatusBadRequest, name+" holds a line break or another control character; paste it as one line.")
		}
	}
	for _, name := range input.Replace {
		if !slices.Contains(names, name) {
			return refuse(http.StatusBadRequest, "Confirm replacing only names you are saving.")
		}
	}
	store, err := s.Options.Credentials()
	if err != nil {
		return refuse(http.StatusServiceUnavailable, "The credential store cannot be opened: "+bounded(err.Error(), 300))
	}
	existing, err := storedNames(store, request.Project, request.Names)
	if err != nil {
		return refuse(http.StatusServiceUnavailable, "The credential store cannot be listed: "+bounded(err.Error(), 300))
	}
	if !slices.Equal(existing, request.Existing) {
		if _, err := s.Store.updateCredential(request.ID, func(r *CredentialRequest) { r.Existing = existing }); err != nil {
			return credentialOutcome{}, err
		}
		s.notify()
	}
	var unconfirmed []string
	for _, name := range names {
		if slices.Contains(existing, name) && !slices.Contains(input.Replace, name) {
			unconfirmed = append(unconfirmed, name)
		}
	}
	if len(unconfirmed) > 0 {
		return credentialOutcome{}, credentialRefusal{status: http.StatusConflict, Message: strings.Join(unconfirmed, ", ") + " already holds a value for " + request.Project + "; confirm replacing it.", Existing: unconfirmed}
	}
	var saved, replaced []string
	var failure error
	for _, name := range names {
		if err := store.Set(auth.Key{Project: request.Project, Name: name}, input.Values[name]); err != nil {
			failure = fmt.Errorf("%s could not be stored: %s", name, bounded(err.Error(), 300))
			break
		}
		saved = append(saved, name)
		if slices.Contains(existing, name) {
			replaced = append(replaced, name)
		}
	}
	if len(saved) == 0 {
		return credentialOutcome{}, failure
	}
	now := time.Now().UTC()
	closed, err := s.Store.updateCredential(request.ID, func(r *CredentialRequest) {
		r.State, r.Saved, r.Replaced, r.ClosedAt = "saved", saved, replaced, &now
		if failure != nil {
			r.Reason = failure.Error()
		}
	})
	if err != nil {
		return credentialOutcome{}, fmt.Errorf("stored %s, but the board could not record it: %w", strings.Join(saved, ", "), err)
	}
	s.credentialWork.Add(1)
	go s.afterCredentialSave(closed)
	s.notify()
	if failure != nil {
		return credentialOutcome{}, fmt.Errorf("stored %s, then %w; the request is closed and the CFO is told", strings.Join(saved, ", "), failure)
	}
	return credentialOutcome{ID: closed.ID, State: closed.State, Saved: saved, Replaced: replaced}, nil
}

// afterCredentialSave runs the refresh cfo auth store runs after it writes,
// so the project's running goblins re-source their auth.ps1, and tells the
// CFO which names were stored for which project, and who was told.
func (s *Service) afterCredentialSave(request CredentialRequest) {
	defer s.credentialWork.Done()
	var told []string
	var refreshErr error
	if s.Options.RefreshCredentials != nil {
		ctx, cancel := context.WithTimeout(context.Background(), credentialRefreshTimeout)
		told, refreshErr = s.Options.RefreshCredentials(ctx, request.Project)
		cancel()
		if _, err := s.Store.updateCredential(request.ID, func(r *CredentialRequest) { r.Told = told }); err != nil {
			s.publish(err)
		}
	}
	detail := strings.Join(request.Saved, ", ") + " stored for " + request.Project + " from the board (" + request.ID + ")"
	switch {
	case len(told) > 0:
		detail += "; told " + strings.Join(told, ", ") + " to re-source auth.ps1"
	case s.Options.RefreshCredentials != nil:
		detail += "; no running " + request.Project + " goblin to tell"
	}
	if request.Reason != "" {
		detail += "; " + request.Reason
	}
	if refreshErr != nil {
		detail += "; the refresh reported: " + bounded(refreshErr.Error(), 500)
	}
	s.credentialNotice(request, detail)
	s.notify()
}

// credentialNotice puts a notice about a request in the CFO's wake queue,
// asking it nothing.
func (s *Service) credentialNotice(request CredentialRequest, detail string) {
	key := request.Task
	if key == "" {
		key = "credentials"
	}
	if _, err := wake.Append(s.Store.Home.State, "review", key, bounded(detail, 4000)); err != nil {
		s.publish(err)
	} else if _, err := wake.PublishEpisode(s.Store.Home.State); err != nil {
		s.publish(err)
	}
}

// expireCredentials closes each request nobody saved within its lifetime and
// tells the CFO, so it can ask again if the values are still needed.
func (s *Service) expireCredentials(now time.Time) error {
	expired, err := s.Store.expireCredentials(now)
	for _, request := range expired {
		s.credentialNotice(request, strings.Join(request.Names, ", ")+" for "+request.Project+": the credential request "+request.ID+" expired unsaved after 24 hours; file it again if the values are still needed")
	}
	return err
}

func (s *Store) expireCredentials(now time.Time) ([]CredentialRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired []CredentialRequest
	for i := range s.db.Credentials {
		if r := &s.db.Credentials[i]; r.State == "open" && !now.Before(r.ExpiresAt) {
			closed := now.UTC()
			r.State, r.Reason, r.ClosedAt = "expired", "Nobody saved it within 24 hours.", &closed
			expired = append(expired, r.clone())
		}
	}
	if len(expired) == 0 {
		return nil, nil
	}
	return expired, s.save()
}

// pruneCredentials drops requests closed for longer than their retention.
func (s *Store) pruneCredentials(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := slices.DeleteFunc(slices.Clone(s.db.Credentials), func(r CredentialRequest) bool {
		return r.State != "open" && r.ClosedAt != nil && now.Sub(*r.ClosedAt) >= closedCredentialRetention
	})
	if len(kept) == len(s.db.Credentials) {
		return nil
	}
	s.db.Credentials = kept
	return s.save()
}
