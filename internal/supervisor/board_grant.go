package supervisor

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// A board's grant is what keeps a browser the Overlord started his after its
// opener is gone. When a browser loads the board while its parents reach the
// desktop, the supervisor gives it a secret, which the browser keeps in a
// cookie its pages cannot read, and the home keeps that secret's hash with
// the browser program it was given to. A later request from that browser,
// whose opener has exited or which has restarted itself, is his by the
// secret.
//
// The secret alone proves nothing, and is not meant to: a cookie for
// 127.0.0.1 goes to every port of it, so any program's local server he opens
// in that browser is handed it. It counts only from the browser program it
// was given to, as that user in that session, with nothing that marks the
// program an agent's or one another program drives (afk_board.go), so a copy
// in an agent's browser, a script or another browser is refused with it. It
// stops counting boardGrantLife after it was given, and only a load whose
// parents reach the desktop gives another, so every grant that counts stands
// on a desktop proof no older than that.

const (
	// boardGrantCookie names the cookie a browser keeps its grant in.
	boardGrantCookie = "cfo-board"
	// boardGrantFile is the home's record of the grants it gave, in its state
	// folder.
	boardGrantFile = "board-grants.json"
	// boardGrantLife is how long a grant counts after it was given.
	boardGrantLife = 30 * 24 * time.Hour
	// boardGrantFresh is how young a browser's grant is left as it is when
	// that browser loads the board again.
	boardGrantFresh = 24 * time.Hour
	// boardGrantsKept bounds the home's record, newest first.
	boardGrantsKept = 64
)

// boardGrant is one grant as the home records it.
type boardGrant struct {
	// Secret is the SHA-256 of the secret the browser holds, so the record
	// hands nobody that secret.
	Secret string `json:"secret"`
	// Image is the browser program the grant was given to.
	Image string    `json:"image"`
	At    time.Time `json:"at"`
}

func (grant boardGrant) counts(now time.Time) bool {
	return !grant.At.After(now) && now.Sub(grant.At) < boardGrantLife
}

// hashedGrant is a grant's secret as the home records it.
func hashedGrant(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// readBoardGrants reads the grants the home gave. A home that gave none has
// no record.
func readBoardGrants(stateDir string) ([]boardGrant, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, boardGrantFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var grants []boardGrant
	if err := json.Unmarshal(data, &grants); err != nil {
		return nil, fmt.Errorf("state/%s cannot be read: %w", boardGrantFile, err)
	}
	return grants, nil
}

// grantOf is the grant the browser that sent r holds for the program image,
// and whether it holds one that counts at now. A secret this home never
// gave, one given to another program and one given too long ago do not.
func (s *Service) grantOf(r *http.Request, image string, now time.Time) (boardGrant, bool, error) {
	cookie, err := r.Cookie(boardGrantCookie)
	if err != nil || cookie.Value == "" {
		return boardGrant{}, false, nil
	}
	s.boardGrants.Lock()
	defer s.boardGrants.Unlock()
	grants, err := readBoardGrants(s.Store.Home.State)
	if err != nil {
		return boardGrant{}, false, err
	}
	held := hashedGrant(cookie.Value)
	at := slices.IndexFunc(grants, func(grant boardGrant) bool {
		return grant.Secret == held && strings.EqualFold(grant.Image, image) && grant.counts(now)
	})
	if at < 0 {
		return boardGrant{}, false, nil
	}
	return grants[at], true, nil
}

// grantBoard records a new grant for the browser program image and returns
// its secret. A record that cannot be read gave grants nobody can check, so
// the new one replaces it.
func (s *Service) grantBoard(image string, now time.Time) (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(random[:])
	s.boardGrants.Lock()
	defer s.boardGrants.Unlock()
	given, _ := readBoardGrants(s.Store.Home.State)
	grants := []boardGrant{{Secret: hashedGrant(secret), Image: image, At: now.UTC()}}
	for _, grant := range given {
		if grant.counts(now) && len(grants) < boardGrantsKept {
			grants = append(grants, grant)
		}
	}
	data, err := json.Marshal(grants)
	if err != nil {
		return "", err
	}
	return secret, fsx.AtomicWriteFile(filepath.Join(s.Store.Home.State, boardGrantFile), data)
}

// grantHisBoard gives the browser that loads the board a grant when the
// supervisor proves the board his by parents that reach the desktop, which is
// the one proof a grant may stand on: a grant never renews itself, and his
// desktop window needs none. A browser whose grant is still fresh keeps it.
// A load that proves nothing gets no grant and loads all the same.
func (h *HTTP) grantHisBoard(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	proven, err := h.Service.proveBoard(r, h.Host, now, askingBoard)
	if err != nil || !proven.fromDesktop {
		return
	}
	if grant, granted, err := h.Service.grantOf(r, proven.image, now); err == nil && granted && now.Sub(grant.At) < boardGrantFresh {
		return
	}
	secret, err := h.Service.grantBoard(proven.image, now)
	if err != nil {
		h.Service.publish(fmt.Errorf("the board in %s could not be given its grant, so it loses the Overlord's switches when its opener exits: %w", proven.started.ExeBase, err))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: boardGrantCookie, Value: secret, Path: "/", MaxAge: int(boardGrantLife / time.Second), HttpOnly: true, SameSite: http.SameSiteStrictMode})
}
