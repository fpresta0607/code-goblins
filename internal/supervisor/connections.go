package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type connectionRequest struct {
	Task       string `json:"task"`
	Generation string `json:"generation"`
	Connection string `json:"connection"`
	Action     string `json:"action"`
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
	respond(w, 200, struct {
		connections.Snapshot
		Instance string `json:"instance"`
	}{checks.Get(meta.ID+"\n"+meta.SpawnGen, refresh), h.Service.Instance})
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
	run := Run{ID: "connection-" + hex.EncodeToString(nonce[:]), Identity: hex.EncodeToString(identity[:]), Title: title, Shell: "powershell", Command: command, Cwd: meta.Worktree, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime), ConnectionTask: meta.ID, ConnectionGeneration: meta.SpawnGen}
	if err := validRun(run); err != nil {
		return Run{}, err
	}
	directory := runDir(s.Store.Home.State, run)
	filename, script := runScript(run)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return Run{}, errors.New("Connection repair could not be saved.")
	}
	if err := fsx.AtomicWriteFile(filepath.Join(directory, filename), script); err != nil {
		return Run{}, errors.New("Connection repair could not be saved.")
	}
	run.ScriptSum = runDigest(script)
	if err := s.Store.acceptRun(run); err != nil {
		return Run{}, err
	}
	return run, nil
}
