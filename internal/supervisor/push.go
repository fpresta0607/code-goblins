package supervisor

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
	"github.com/fpresta0607/code-goblins/internal/waiting"
	"github.com/fpresta0607/code-goblins/internal/webpush"
)

// The Overlord's Home Screen app carries the count of what waits on him on
// its icon while it is closed, from a push the supervisor sends it with each
// item newly waiting (CFO decision 1008918: the badge count and his Command
// Center items, nothing else). A count that falls is corrected when the app
// opens (CFO decision 1008953).
const (
	// pushFile keeps the board's VAPID keys and each app that turned its
	// badge on, in the state folder, readable by its owner alone.
	pushFile = "push.json"
	// maxPushApps bounds the apps kept: a new one replaces the oldest.
	maxPushApps = 8
	// pushContact is who runs the server, which a push's authorization names.
	pushContact = "https://github.com/fpresta0607/code-goblins"
	// pushTTL is how long a push service holds a push for an app that is
	// away.
	pushTTL = 12 * time.Hour
	// maxPushesAtOnce bounds the items one change pushes.
	maxPushesAtOnce = 3
	// pushTitleLimit bounds a push's title, its asker's name.
	pushTitleLimit = 60
)

// pushRecord is what pushFile holds.
type pushRecord struct {
	Keys []byte                 `json:"keys"`
	Apps []webpush.Subscription `json:"apps"`
}

// readPush reads pushFile, or nothing kept when there is none. The caller
// holds pushMu.
func (s *Service) readPush() (pushRecord, error) {
	var record pushRecord
	data, err := fsx.ReadFile(filepath.Join(s.Store.Home.State, pushFile))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return record, nil
	case err != nil:
		return record, err
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return pushRecord{}, fmt.Errorf("%s cannot be read: %w", pushFile, err)
	}
	return record, nil
}

// writePush keeps record, which holds a private key, for its owner alone.
// The caller holds pushMu.
func (s *Service) writePush(record pushRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return auth.WriteSecretFile(filepath.Join(s.Store.Home.State, pushFile), string(data))
}

// pushKeys is the board's VAPID key pair, made and kept the first time an
// app asks for it. The caller holds pushMu.
func (s *Service) pushKeys() (webpush.Keys, pushRecord, error) {
	record, err := s.readPush()
	if err != nil {
		return webpush.Keys{}, record, err
	}
	if len(record.Keys) > 0 {
		keys, err := webpush.ParseKeys(record.Keys)
		return keys, record, err
	}
	keys, err := webpush.GenerateKeys()
	if err == nil {
		record.Keys, err = keys.Marshal()
	}
	if err == nil {
		err = s.writePush(record)
	}
	return keys, record, err
}

// pushApp is an app's subscription as the board's page sends it, from
// PushSubscription.toJSON.
type pushApp struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// subscription is app's subscription if it is one a browser made through a
// push service browsers use.
func (app pushApp) subscription() (webpush.Subscription, error) {
	key, keyErr := base64.RawURLEncoding.DecodeString(strings.TrimRight(app.Keys.P256dh, "="))
	if keyErr == nil {
		_, keyErr = ecdh.P256().NewPublicKey(key)
	}
	secret, secretErr := base64.RawURLEncoding.DecodeString(strings.TrimRight(app.Keys.Auth, "="))
	switch {
	case len(app.Endpoint) > 2048 || !webpush.Endpoint(app.Endpoint):
		return webpush.Subscription{}, errors.New("This browser's push service is not one Code Goblins sends to")
	case keyErr != nil || secretErr != nil || len(secret) != 16:
		return webpush.Subscription{}, errors.New("This browser's push keys cannot be read")
	}
	return webpush.Subscription{Endpoint: app.Endpoint, P256dh: app.Keys.P256dh, Auth: app.Keys.Auth}, nil
}

// push serves /api/push: GET is the key an app subscribes with, and POST
// keeps an app's subscription when he turns its badge on, which only his
// Home Screen app on the private board does, or drops it when he turns it
// off, from anywhere.
func (h *HTTP) push(w http.ResponseWriter, r *http.Request) {
	s := h.Service
	if s.Options.Push == nil {
		apiError(w, http.StatusConflict, "The app badge is unavailable on this board")
		return
	}
	var input struct {
		Subscribe   *pushApp `json:"subscribe"`
		Unsubscribe *string  `json:"unsubscribe"`
	}
	if r.Method == "POST" {
		if err := decodeBody(w, r, &input, 8192); err != nil || (input.Subscribe == nil) == (input.Unsubscribe == nil) {
			apiError(w, http.StatusBadRequest, "Say which app's badge to turn on or off")
			return
		}
		if input.Subscribe != nil && offMachine(r, h.Host) == "" {
			apiError(w, http.StatusForbidden, "Turn the app badge on in the Home Screen app on your phone")
			return
		}
	}
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	keys, record, err := s.pushKeys()
	if err != nil {
		apiError(w, http.StatusInternalServerError, "The app badge's settings cannot be read")
		return
	}
	answer := struct {
		Key string `json:"key"`
	}{keys.Public()}
	if r.Method == "GET" {
		respond(w, http.StatusOK, answer)
		return
	}
	if input.Unsubscribe != nil {
		record.Apps = slices.DeleteFunc(record.Apps, func(kept webpush.Subscription) bool { return kept.Endpoint == *input.Unsubscribe })
	} else {
		app, err := input.Subscribe.subscription()
		if err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		record.Apps = append(slices.DeleteFunc(record.Apps, func(kept webpush.Subscription) bool { return kept.Endpoint == app.Endpoint }), app)
		record.Apps = record.Apps[max(0, len(record.Apps)-maxPushApps):]
	}
	if err := s.writePush(record); err != nil {
		apiError(w, http.StatusInternalServerError, "The app badge's settings cannot be saved")
		return
	}
	s.notify()
	respond(w, http.StatusOK, answer)
}

// keepPushing pushes each item newly waiting on the Overlord, as the desktop
// window notifies it, with how many wait, to every app that turned its badge
// on, as the board changes. What it meets waits in pushErr for the next
// recovery cycle to report, once, since a report changes the board and so
// would wake it again.
func (s *Service) keepPushing(ctx context.Context) {
	changes, stop := s.subscribe()
	defer stop()
	var seen map[string]bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
		}
		seen = s.pushChange(ctx, seen)
	}
}

// pushChange is one change's pushes. What it meets waits in pushErr, and it
// changes nothing on the board itself, so it never wakes keepPushing again.
func (s *Service) pushChange(ctx context.Context, seen map[string]bool) map[string]bool {
	seen, err := s.pushNew(ctx, seen)
	if err != nil {
		s.mu.Lock()
		if s.pushErr == nil || !strings.Contains(s.pushErr.Error(), err.Error()) {
			s.pushErr = errors.Join(s.pushErr, fmt.Errorf("the app badge: %w", err))
		}
		s.mu.Unlock()
	}
	return seen
}

// pushNew pushes what waits on the Overlord that seen did not hold, and
// returns what waits now, or nil while no app has its badge on. The first
// look, and the first once an app turned its badge on, only learns what
// already waits; under AFK mode nothing is pushed, as nothing is announced.
// Remote access off forgets every app, since each turned its badge on over
// the private board, so a phone he lost or let go stops hearing at once.
func (s *Service) pushNew(ctx context.Context, seen map[string]bool) (map[string]bool, error) {
	s.pushMu.Lock()
	record, err := s.readPush()
	s.pushMu.Unlock()
	if err != nil || len(record.Apps) == 0 {
		return nil, err
	}
	remote, err := remoteaccess.Read(s.Store.Home.State)
	if err != nil {
		return seen, err
	}
	if !remote.Enabled {
		return nil, s.forgetPushApps(func(webpush.Subscription) bool { return true })
	}
	// What waits is read from the store's memory, at once; the snapshot, which
	// names the goblins that ask, is read only for something new to push.
	items, _ := s.Items()
	data, err := json.Marshal(struct {
		Items
		At time.Time `json:"at"`
	}{items, time.Now().UTC()})
	if err != nil {
		return seen, err
	}
	_, waits, err := waiting.Items(data)
	if err != nil {
		return seen, err
	}
	current := map[string]bool{}
	isNew := false
	for _, item := range waits {
		current[item.ID] = true
		isNew = isNew || seen != nil && !seen[item.ID]
	}
	if !isNew || s.afkOn() {
		return current, nil
	}
	snapshot, err := s.SnapshotSince(s.Revision())
	if err == nil {
		data, err = json.Marshal(snapshot)
	}
	if err == nil {
		_, waits, err = waiting.Items(data)
	}
	if err != nil {
		return seen, err
	}
	s.pushMu.Lock()
	keys, err := webpush.ParseKeys(record.Keys)
	s.pushMu.Unlock()
	if err != nil {
		return current, err
	}
	var fresh []waiting.Item
	for _, item := range waits {
		if !seen[item.ID] && !item.IsQuiet {
			fresh = append(fresh, item)
		}
	}
	// An app this board did not keep, its push service not one it sends to,
	// as from a hand edit of pushFile, is forgotten, never sent to.
	var errs error
	if slices.ContainsFunc(record.Apps, func(app webpush.Subscription) bool { return !webpush.Endpoint(app.Endpoint) }) {
		errs = errors.Join(errors.New(pushFile+" named an app whose push service Code Goblins does not send to, which is forgotten"),
			s.forgetPushApps(func(app webpush.Subscription) bool { return !webpush.Endpoint(app.Endpoint) }))
		record.Apps = slices.DeleteFunc(record.Apps, func(app webpush.Subscription) bool { return !webpush.Endpoint(app.Endpoint) })
	}
	for _, item := range fresh[:min(len(fresh), maxPushesAtOnce)] {
		title := item.Asker
		if utf8.RuneCountInString(title) > pushTitleLimit {
			title = string([]rune(title)[:pushTitleLimit-1]) + "…"
		}
		payload, err := json.Marshal(struct {
			Title string `json:"title"`
			Body  string `json:"body"`
			Count int    `json:"count"`
			Tag   string `json:"tag"`
		}{title, item.Text, len(waits), item.ID})
		if err != nil {
			return current, err
		}
		for _, app := range record.Apps {
			err := webpush.Send(ctx, s.Options.Push, keys, pushContact, app, payload, pushTTL)
			if errors.Is(err, webpush.ErrGone) {
				err = s.forgetPushApps(func(kept webpush.Subscription) bool { return kept.Endpoint == app.Endpoint })
			}
			errs = errors.Join(errs, err)
		}
	}
	return current, errs
}

// forgetPushApps forgets each app forget names.
func (s *Service) forgetPushApps(forget func(webpush.Subscription) bool) error {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	record, err := s.readPush()
	if err != nil {
		return err
	}
	kept := len(record.Apps)
	record.Apps = slices.DeleteFunc(record.Apps, forget)
	if len(record.Apps) == kept {
		return nil
	}
	return s.writePush(record)
}
