package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/onboarding"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type RecoveryResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func (h *HTTP) resumeTasks(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	recovery := h.Service.Options.TaskRecovery
	if recovery == nil {
		apiError(w, http.StatusConflict, "Task recovery is unavailable on this board")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	results, err := recovery.Run(ctx, h.Service.Store.Snapshot())
	if err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	h.Service.notify()
	respond(w, http.StatusOK, results)
}

type TaskRecovery struct {
	State     string
	Tasks     func() ([]state.TaskMeta, error)
	IsRunning func(context.Context, state.TaskMeta) (bool, error)
	Memory    func() (uint64, uint64, error)
	Resume    func(context.Context, state.TaskMeta, string) error
	mu        sync.Mutex
}

func (r *TaskRecovery) Run(ctx context.Context, database Database) ([]RecoveryResult, error) {
	if !r.mu.TryLock() {
		return nil, errors.New("task recovery is already running")
	}
	defer r.mu.Unlock()
	tasks, err := r.Tasks()
	if err != nil {
		return nil, err
	}
	results := make([]RecoveryResult, 0, len(tasks))
	for _, task := range tasks {
		result := RecoveryResult{ID: task.ID, Status: "Needs a hand"}
		if r.State != "" {
			var err error
			task, err = state.ReadTaskMeta(r.State, task.ID)
			if err != nil {
				result.Detail = "Task record could not be read: " + err.Error()
				results = append(results, result)
				continue
			}
		}
		isRunning, err := r.IsRunning(ctx, task)
		switch {
		case err != nil:
			result.Detail = "Could not check whether this task runs: " + err.Error()
		case isRunning:
			result.Status = "Already running"
			result.Detail = "Kept the existing terminal and worktree."
		default:
			node := database.Sessions[database.TaskSessions[task.ID]]
			session := task.ResumeSession
			if node.TaskID == task.ID && node.Harness == task.Harness && node.Generation == task.SpawnGen {
				session = node.NativeID
			}
			if session == "" {
				result.Detail = "No exact conversation is recorded for this task generation. Open its agent and select the conversation in " + task.Worktree
			} else if _, err := onboarding.ResumeArgs(task.Harness, session); err != nil {
				result.Detail = err.Error()
			} else {
				available, _, err := r.Memory()
				switch {
				case err != nil:
					result.Detail = "Free memory could not be checked: " + err.Error()
				case available < 5<<30:
					result.Status = "Waiting for memory"
					result.Detail = "Run goblins resume again with at least 5 GB available."
				default:
					if err := r.Resume(ctx, task, session); err != nil {
						result.Detail = err.Error()
					} else {
						result.Status = "Resumed"
						result.Detail = fmt.Sprintf("%s, model %s, effort %s, in %s", task.Harness, task.Model, task.Effort, task.Worktree)
					}
				}
			}
		}
		results = append(results, result)
	}
	return results, nil
}
