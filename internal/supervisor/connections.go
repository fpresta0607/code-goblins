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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

type connectionRequest struct {
	Task              string `json:"task"`
	Generation        string `json:"generation"`
	Connection        string `json:"connection"`
	Action            string `json:"action"`
	Request           string `json:"request,omitempty"`
	RequestGeneration string `json:"request_generation,omitempty"`
}

func (s *Service) connections() (*connections.Cache, *connections.Inspector) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connectionInspector == nil {
		s.connectionInspector = connections.NewInspector(s.Store.Home.Data, s.Store.Home.State)
	}
	if s.connectionChecks == nil {
		inspector := s.connectionInspector
		s.connectionChecks = connections.NewCache(time.Minute, 45*time.Second, func(ctx context.Context, key string) connections.Snapshot {
			task, generation, _ := strings.Cut(key, "\n")
			meta, err := s.connectionTask(task, generation)
			if err != nil {
				return connections.Snapshot{Error: err.Error(), Entries: []connections.Entry{}}
			}
			if err := s.verifyWorkspace(ctx, meta); err != nil {
				return connections.Snapshot{Error: err.Error(), Entries: []connections.Entry{}}
			}
			return inspector.Check(ctx, meta)
		})
	}
	return s.connectionChecks, s.connectionInspector
}

func (s *Service) connectionTask(task, generation string) (state.TaskMeta, error) {
	meta, err := state.ReadTaskMeta(s.Store.Home.State, task)
	if err != nil || generation == "" || meta.SpawnGen != generation {
		return state.TaskMeta{}, errors.New("Task restarted or was replaced. Refresh connections.")
	}
	return meta, nil
}

func (h *HTTP) connectionResponse(w http.ResponseWriter, meta state.TaskMeta, refresh bool) {
	checks, _ := h.Service.connections()
	logins := []browserLoginEntry{}
	if h.Service.browsers != nil {
		var err error
		if logins, err = h.Service.browsers.projectLogins(auth.ProjectName(meta.Project)); err != nil {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	respond(w, 200, struct {
		connections.Snapshot
		Instance      string              `json:"instance"`
		BrowserLogins []browserLoginEntry `json:"browser_logins"`
	}{checks.Get(meta.ID+"\n"+meta.SpawnGen, refresh), h.Service.Instance, logins})
}

func (h *HTTP) readConnections(w http.ResponseWriter, r *http.Request) {
	meta, err := h.Service.connectionTask(r.URL.Query().Get("task"), r.URL.Query().Get("generation"))
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	h.connectionResponse(w, meta, false)
}

func decodeConnection(r *http.Request) (connectionRequest, error) {
	var input connectionRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4097))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, errors.New("Choose a connection from this goblin.")
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return input, errors.New("Choose a connection from this goblin.")
	}
	return input, nil
}

func (h *HTTP) refreshConnections(w http.ResponseWriter, r *http.Request) {
	input, err := decodeConnection(r)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	meta, err := h.Service.connectionTask(input.Task, input.Generation)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	h.connectionResponse(w, meta, true)
}

func (h *HTTP) fixConnection(w http.ResponseWriter, r *http.Request) {
	input, err := decodeConnection(r)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	meta, err := h.Service.connectionTask(input.Task, input.Generation)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	checks, inspector := h.Service.connections()
	snapshot := checks.Cached(meta.ID + "\n" + meta.SpawnGen)
	if snapshot.Checking || snapshot.CheckedAt.IsZero() {
		apiError(w, 409, "Wait for the connection check to finish.")
		return
	}
	plan, err := inspector.RepairPlan(meta, snapshot, input.Connection, input.Action)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	if plan.URL != "" {
		respond(w, 200, map[string]string{"url": plan.URL, "message": "Complete sign-in, then recheck the connection."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := h.Service.verifyWorkspace(ctx, meta); err != nil {
		apiError(w, 409, err.Error())
		return
	}
	run, err := h.Service.connectionRun(meta, input, plan)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	h.Service.notify()
	respond(w, 200, map[string]string{"run_id": run.ID, "message": "Your repair is ready in Command Center. Run it there to continue."})
}

// grantingBoard asks for a connector card's + or check: giving a goblin a
// service or an MCP server, or taking it back, is the Overlord's from a board
// of his own, or the CFO's with cfo auth.
var grantingBoard = boardAsker("Giving a goblin a connector is the Supreme Overlord's", ": he presses it on a board of his own, or the CFO runs cfo auth grant or cfo auth revoke", "Change it in the Code Goblins window, or ask the CFO.", reopenTheWindow+"change it there.")

// grantConnection gives a running task one of its project's services or MCP
// servers from its connector card, or takes it back: what cfo auth grant and
// cfo auth revoke do. Past the board's Host, Origin and token checks it takes
// only a board proven to be the Overlord's own, since a goblin can read the
// token. The task's status names him, the CFO is told by a wake, and a running
// terminal of the task is told to load a granted service's credentials, as cfo
// auth grant tells it. The check that follows shows the card's new state.
func (h *HTTP) grantConnection(w http.ResponseWriter, r *http.Request) {
	asked := time.Now()
	input, err := decodeConnection(r)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	kind, name, _ := strings.Cut(input.Connection, ":")
	isGrant := input.Action == "grant"
	if !isGrant && input.Action != "revoke" || name == "" || kind != "service" && kind != "mcp" && kind != "withheld" {
		apiError(w, 400, "Choose a connector this goblin can be given or have taken back.")
		return
	}
	s := h.Service
	// A service grant card's Grant is his from his private board too: his
	// decision of 2026-10-29 ("Allow it as is"), knowing the gate proves that
	// board his only by headers a program on this PC can forge. Every other
	// press here stays on a board on the PC.
	var from string
	if remoteaccess.Admitted(r) && kind == "service" && isGrant && input.Request != "" {
		from = privateBoard
	} else {
		from, err = s.overlordsBoard(r, h.Host, asked, grantingBoard)
	}
	if err != nil {
		h.refuse(w, http.StatusForbidden, "connections", "A press of a connector card on a board was refused", "", err)
		return
	}
	meta, err := s.connectionTask(input.Task, input.Generation)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	var card CredentialRequest
	if input.Request != "" || input.RequestGeneration != "" {
		s.credentialSaves.Lock()
		defer s.credentialSaves.Unlock()
		card, err = s.openCredential(input.Request, input.RequestGeneration)
		if err != nil || !isGrant || kind != "service" || card.GrantService != name || card.Task != meta.ID || card.TaskGeneration != meta.SpawnGen {
			apiError(w, http.StatusConflict, "This grant does not match an open request for this task and service.")
			return
		}
		if _, err := s.credentialGrantTask(card); err != nil {
			apiError(w, http.StatusConflict, err.Error())
			return
		}
	}
	stateDir := s.Store.Home.State
	refresher := spawn.AuthRefresher{StateDir: stateDir, DataDir: s.Store.Home.Data, Panes: spawn.NativeLiveness{StateDir: stateDir}, By: "the Overlord from " + from, Generation: meta.SpawnGen, Project: auth.ProjectName(meta.Project)}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var item spawn.Refreshed
	switch {
	case kind == "service" && isGrant:
		item, _, err = refresher.Grant(ctx, meta.ID, []string{name})
	case kind == "service":
		_, _, err = refresher.Revoke(ctx, meta.ID, []string{name})
	case isGrant:
		_, err = refresher.GrantMCP(meta.ID, []string{name})
	default:
		_, err = refresher.RevokeMCP(meta.ID, []string{name})
	}
	// A change that stands is told and shown as one, whatever failed after it.
	if err != nil && !spawn.ChangeStands(err) {
		apiError(w, 409, err.Error())
		return
	}
	told := "connections: the Overlord " + map[bool]string{true: "granted", false: "revoked"}[isGrant] + " " + name + " from " + from
	if err != nil {
		told += ", but " + err.Error()
	}
	if card.ID != "" {
		now := time.Now().UTC()
		closed, closeErr := s.Store.updateCredential(card.ID, func(request *CredentialRequest) {
			request.State, request.ClosedAt = "granted", &now
			if err != nil {
				request.Reason = err.Error()
			}
		})
		if closeErr != nil {
			s.publish(closeErr)
		} else if waitErr := s.Store.closeWaitsForCard(closed, "overlord"); waitErr != nil {
			s.publish(waitErr)
		}
		s.notify()
	}
	if _, err := wake.Append(stateDir, "notify", meta.ID, told); err != nil {
		s.publish(err)
	} else if _, err := wake.PublishEpisode(stateDir); err != nil {
		s.publish(err)
	}
	if item.Live && meta.Backend == "native" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			// A task replaced since the press is told nothing of the grant.
			if current, err := state.ReadTaskMeta(stateDir, meta.ID); err != nil || current.SpawnGen != meta.SpawnGen {
				return
			}
			if err := typeIntoGoblin(ctx, stateDir, meta, goblinLine("credentials refreshed: re-source "+item.Path), false); err != nil {
				s.publish(fmt.Errorf("telling %s to load the credentials the Overlord granted: %w", meta.ID, err))
			}
		}()
	}
	h.connectionResponse(w, meta, true)
}

func (s *Service) connectionRun(meta state.TaskMeta, input connectionRequest, plan connections.Repair) (Run, error) {
	s.runRequests.Lock()
	defer s.runRequests.Unlock()
	if _, err := s.connectionTask(meta.ID, meta.SpawnGen); err != nil {
		return Run{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return Run{}, errors.New("Connection repair is unavailable.")
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	command := "$ErrorActionPreference = 'Stop'\n$env:CFO_HOME = " + quote(s.Store.Home.Root) + "\n$env:CFO_STATE_OVERRIDE = " + quote(s.Store.Home.State) + "\n"
	title := "Sign in to " + strings.TrimPrefix(input.Connection, "service:")
	if plan.Credential != "" {
		title = "Store " + plan.Credential + " from clipboard for " + filepath.Base(meta.Project)
		command += "Get-Clipboard -Raw | & " + quote(executable) + " auth store --project " + quote(meta.Project) + " " + quote(plan.Credential) + "\nexit $LASTEXITCODE\n"
	} else {
		command += "& " + quote(executable) + " connection-repair " + quote(meta.ID) + " " + quote(meta.SpawnGen) + " " + quote(input.Connection) + " " + quote(input.Action) + "\nexit $LASTEXITCODE\n"
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Run{}, errors.New("Connection repair is unavailable.")
	}
	identity := sha256.Sum256([]byte(s.Instance + "\n" + meta.ID + "\n" + meta.SpawnGen))
	now := time.Now().UTC()
	return s.recordBoardRun(Run{ID: "connection-" + hex.EncodeToString(nonce[:]), Identity: hex.EncodeToString(identity[:]), Title: title, Shell: "powershell", Command: command, Cwd: meta.Worktree, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime), ConnectionTask: meta.ID, ConnectionGeneration: meta.SpawnGen})
}

// recordBoardRun records a run item the board made itself, with the script
// file Run executes written beside it first.
func (s *Service) recordBoardRun(run Run) (Run, error) {
	if err := validRun(run); err != nil {
		return Run{}, err
	}
	directory := runDir(s.Store.Home.State, run)
	filename, script := runScript(run)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return Run{}, errors.New("The run item could not be saved.")
	}
	if err := fsx.AtomicWriteFile(filepath.Join(directory, filename), script); err != nil {
		return Run{}, errors.New("The run item could not be saved.")
	}
	run.ScriptSum = runDigest(script)
	if err := s.Store.acceptRun(run); err != nil {
		return Run{}, err
	}
	return run, nil
}
