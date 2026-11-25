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
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/remoteaccess"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// connectionRequest is a press on a goblin's connector: the task and its
// generation, the connection and what is asked of it.
type connectionRequest struct {
	Task              string `json:"task"`
	Generation        string `json:"generation"`
	Connection        string `json:"connection"`
	Action            string `json:"action"`
	Request           string `json:"request,omitempty"`
	RequestGeneration string `json:"request_generation,omitempty"`
}

// connectorRefreshRequest asks for one level to be checked again.
type connectorRefreshRequest struct {
	Level string `json:"level"`
	Scope string `json:"scope"`
}

// connectorFixRequest asks for a project's connection to be repaired.
type connectorFixRequest struct {
	Project    string `json:"project"`
	Connection string `json:"connection"`
	Action     string `json:"action"`
}

// connectorsFolder keeps the Connectors page's snapshots under the state
// folder.
const connectorsFolder = "connectors"

// connectionChecksFile is what an earlier build kept each goblin's check in,
// which nothing reads now.
const connectionChecksFile = "connection-checks.json"

// connectorsTick is how often the snapshots look for what changed.
const connectorsTick = 15 * time.Second

// connectors is the Connectors page's snapshots, made on first use.
func (s *Service) connectors() *connections.Snapshots {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connectionSnapshots == nil {
		home := s.Store.Home
		userHome, _ := os.UserHomeDir()
		environment := connections.NewEnvironment(home.Root, userHome, home.Data, home.State, func() string { return os.Getenv(install.ProjectsRootVariable) })
		snapshots := connections.NewSnapshots(environment, filepath.Join(home.State, connectorsFolder))
		snapshots.Failed = func(err error) {
			_ = s.boardErrors.report(home.State, "keeping connector snapshots", err.Error(), time.Now())
		}
		s.connectionSnapshots = snapshots
	}
	return s.connectionSnapshots
}

// startConnectors lets go of the file an earlier build kept and starts the
// snapshots' watch, which ends with the service.
func (s *Service) startConnectors(ctx context.Context) {
	_ = fsx.Remove(filepath.Join(s.Store.Home.State, connectionChecksFile))
	snapshots := s.connectors()
	s.connectorWork.Add(1)
	go func() {
		defer s.connectorWork.Done()
		snapshots.Watch(ctx, connectorsTick, s.runningTasks)
	}()
}

// closeConnectors ends the snapshots' watch and background check.
func (s *Service) closeConnectors() {
	s.connectorWork.Wait()
	s.mu.Lock()
	snapshots := s.connectionSnapshots
	s.mu.Unlock()
	if snapshots != nil {
		snapshots.Close()
	}
}

// runningTasks are the goblins whose records say they run: a paused or stopped
// one has no session to read until it resumes.
func (s *Service) runningTasks() []state.TaskMeta {
	var running []state.TaskMeta
	for _, meta := range liveTasks(s.Store.Home.State) {
		if record, err := s.lifecycle(meta.ID); err != nil || record.LeavesRunning(s.Store.Home.State, meta) {
			running = append(running, meta)
		}
	}
	return running
}

func (s *Service) connectionTask(task, generation string) (state.TaskMeta, error) {
	meta, err := state.ReadTaskMeta(s.Store.Home.State, task)
	if err != nil || generation == "" || meta.SpawnGen != generation {
		return state.TaskMeta{}, errors.New("Task restarted or was replaced. Refresh connections.")
	}
	return meta, nil
}

// connectorProject is a project of the page with the browser logins its
// goblins use.
type connectorProject struct {
	connections.Project
	BrowserLogins []browserLoginEntry `json:"browser_logins"`
}

// connectorsResponse answers with the snapshots as they stand: nothing is
// checked to answer it.
func (h *HTTP) connectorsResponse(w http.ResponseWriter) {
	view := h.Service.connectors().Read()
	projects := make([]connectorProject, 0, len(view.Projects))
	for _, project := range view.Projects {
		logins := []browserLoginEntry{}
		if h.Service.browsers != nil {
			var err error
			if logins, err = h.Service.browsers.projectLogins(project.Name); err != nil {
				apiError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		projects = append(projects, connectorProject{project, logins})
	}
	respond(w, 200, struct {
		Instance string               `json:"instance"`
		Machine  connections.Level    `json:"machine"`
		Projects []connectorProject   `json:"projects"`
		Goblins  []connections.Goblin `json:"goblins"`
	}{h.Service.Instance, view.Machine, projects, view.Goblins})
}

func (h *HTTP) readConnectors(w http.ResponseWriter, _ *http.Request) { h.connectorsResponse(w) }

// decodeConnectorRequest reads one request body that names only the fields of
// value, so a body that carries anything else, a credential's value among it,
// is refused as it reads, whichever field it travels in.
func decodeConnectorRequest(r *http.Request, value any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4097))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return errors.New("more than one request")
	}
	return nil
}

func decodeConnection(r *http.Request) (connectionRequest, error) {
	var input connectionRequest
	if err := decodeConnectorRequest(r, &input); err != nil {
		return input, errors.New("Choose a connection from this goblin.")
	}
	return input, nil
}

// refreshConnectors queues a recheck of the machine, one project or one goblin
// and answers at once with the snapshots, which say it is being checked.
func (h *HTTP) refreshConnectors(w http.ResponseWriter, r *http.Request) {
	var input connectorRefreshRequest
	if err := decodeConnectorRequest(r, &input); err != nil {
		apiError(w, 400, "Choose the machine, a project or a goblin to recheck.")
		return
	}
	if err := h.Service.connectors().Refresh(input.Level, input.Scope); err != nil {
		code := http.StatusNotFound
		if errors.Is(err, connections.ErrUnknownLevel) {
			code = http.StatusBadRequest
		}
		apiError(w, code, err.Error())
		return
	}
	h.connectorsResponse(w)
}

// fixConnector makes the repair a project's connection offers: its sign-in
// page, or a card in Command Center that stores a credential from the
// clipboard or runs the service's login.
func (h *HTTP) fixConnector(w http.ResponseWriter, r *http.Request) {
	var input connectorFixRequest
	if err := decodeConnectorRequest(r, &input); err != nil {
		apiError(w, 400, "Choose a connection from this project.")
		return
	}
	snapshots := h.Service.connectors()
	projects := snapshots.Read().Projects
	index := slices.IndexFunc(projects, func(project connections.Project) bool { return project.Name == input.Project })
	if index < 0 {
		apiError(w, 409, "This project is not listed. Refresh connections.")
		return
	}
	project := projects[index]
	if project.Checking || project.CheckedAt.IsZero() {
		apiError(w, 409, "Wait for the connection check to finish.")
		return
	}
	plan, err := snapshots.Env.RepairPlan(project, input.Connection, input.Action)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	if plan.URL != "" {
		respond(w, 200, map[string]string{"url": plan.URL, "message": "Complete sign-in, then recheck the connection."})
		return
	}
	if info, err := os.Stat(project.Path); err != nil || !info.IsDir() {
		apiError(w, 409, "This project's checkout was not found.")
		return
	}
	run, err := h.Service.connectionRun(project, input, plan)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	h.Service.notify()
	respond(w, 200, map[string]string{"run_id": run.ID, "message": "Your repair is ready in Command Center. Run it there to continue."})
}

// grantingBoard asks for a connector's Connect or Disconnect: giving a goblin
// a service or an MCP server, or taking it back, is the Overlord's from a
// board of his own, or the CEO's with goblins auth.
var grantingBoard = boardAsker("Giving a goblin a connector is the Supreme Overlord's", ": he presses it on a board of his own, or the CEO runs goblins auth grant or goblins auth revoke", "Change it in the Code Goblins window, or ask the CEO.", reopenTheWindow+"change it there.")

// grantConnection gives a running task one of its project's services or MCP
// servers from the board's Connectors page, or takes it back: what goblins auth
// grant and goblins auth revoke do. Past the board's Host, Origin and token checks it takes
// only a board proven to be the Overlord's own, since a goblin can read the
// token. The task's status names him, the CEO is told by a wake, and a running
// terminal of the task is told to load a granted service's credentials, as goblins
// auth grant tells it. The goblin is read again from its record, and the answer
// is the page's snapshots, which show the goblin's new state.
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
		h.refuse(w, http.StatusForbidden, "connections", "A press of a connector's button on a board was refused", "", err)
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
		s.credentialWork.Add(1)
		go func() {
			defer s.credentialWork.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			// A task replaced since the press is told nothing of the grant.
			if current, err := state.ReadTaskMeta(stateDir, meta.ID); err != nil || current.SpawnGen != meta.SpawnGen {
				return
			}
			// A goblin in a turn has its harness hold the notice for its next
			// tool call, so the notice still arrives (#2015).
			if err := typeIntoGoblin(ctx, stateDir, meta, goblinLine("credentials refreshed: re-source "+item.Path), false, false, false); err != nil && !errors.Is(err, fleet.ErrQueuedForToolCall) {
				s.publish(fmt.Errorf("telling %s to load the credentials the Overlord granted: %w", meta.ID, err))
			}
		}()
	}
	// The goblin is read again from the record the change left, so the answer
	// shows the grant, not the goblin as it was before it.
	changed := meta
	if current, err := s.connectionTask(meta.ID, meta.SpawnGen); err == nil {
		changed = current
	}
	s.connectors().ReadNow(changed)
	h.connectorsResponse(w)
}

// connectionRun makes the card in Command Center that does a repair for a
// project: the sign-in command of a service, or the store of what he copied.
func (s *Service) connectionRun(project connections.Project, input connectorFixRequest, plan connections.Repair) (Run, error) {
	s.runRequests.Lock()
	defer s.runRequests.Unlock()
	executable, err := os.Executable()
	if err != nil {
		return Run{}, errors.New("Connection repair is unavailable.")
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	command := "$ErrorActionPreference = 'Stop'\n$env:GOBLINS_HOME = " + quote(s.Store.Home.Root) + "\n$env:GOBLINS_STATE_OVERRIDE = " + quote(s.Store.Home.State) + "\n"
	title := "Sign in to " + strings.TrimPrefix(input.Connection, "service:")
	if plan.Credential != "" {
		title = "Store " + plan.Credential + " from clipboard for " + project.Name
		command += "Get-Clipboard -Raw | & " + quote(executable) + " auth store --project " + quote(project.Path) + " " + quote(plan.Credential) + "\nexit $LASTEXITCODE\n"
	} else {
		command += "& " + quote(executable) + " connection-repair " + quote(project.Path) + " " + quote(input.Connection) + " " + quote(input.Action) + "\nexit $LASTEXITCODE\n"
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Run{}, errors.New("Connection repair is unavailable.")
	}
	identity := sha256.Sum256([]byte(s.Instance + "\n" + project.Name + "\n" + project.Path))
	now := time.Now().UTC()
	return s.recordBoardRun(Run{ID: "connection-" + hex.EncodeToString(nonce[:]), Identity: hex.EncodeToString(identity[:]), Title: title, Shell: "powershell", Command: command, Cwd: project.Path, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime), ConnectionProject: project.Name})
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
