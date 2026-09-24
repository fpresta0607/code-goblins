package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type cachedResponse struct {
	body []byte
	at   time.Time
}
type HTTP struct {
	Service       *Service
	Host          string
	Assets        fs.FS
	mu            sync.Mutex
	cache         map[string]cachedResponse
	gitSlots      chan struct{}
	streams       chan struct{}
	terminalSlots chan struct{}
	terminals     map[string]*terminalLease
	openTerminal  func(context.Context, string, string, bool, int, int) (herdr.TerminalStream, error)
	terminalTick  time.Duration
	editor        execx.Starter
	editorLookup  func(string) (string, error)
}

func NewHTTP(s *Service, host string, assets fs.FS) *HTTP {
	return &HTTP{Service: s, Host: host, Assets: assets, cache: map[string]cachedResponse{}, gitSlots: make(chan struct{}, 2), streams: make(chan struct{}, 8), terminalSlots: make(chan struct{}, 4), terminals: map[string]*terminalLease{}, openTerminal: herdr.OpenTerminal, terminalTick: 5 * time.Second, editor: execx.OSRunner{}, editorLookup: exec.LookPath}
}

func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	w.Header().Set("Cache-Control", "no-store")
	if r.Host != h.Host {
		apiError(w, 403, "Untrusted Host")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+h.Host {
		apiError(w, 403, "Untrusted origin")
		return
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		apiError(w, 403, "Cross-site request refused")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		if r.Header.Get("Origin") != "http://"+h.Host || r.Header.Get("X-CFO-Token") != h.Service.Instance {
			apiError(w, 403, "Refresh the board before submitting an action")
			return
		}
	}
	switch {
	case r.URL.Path == "/api/snapshot" && r.Method == "GET":
		snapshot, err := h.Service.Snapshot()
		if err != nil {
			apiError(w, 503, err.Error())
			return
		}
		respond(w, 200, snapshot)
	case r.URL.Path == "/api/events" && r.Method == "GET":
		h.stream(w, r)
	case r.URL.Path == "/api/terminal/stream" && r.Method == "POST":
		h.terminalStream(w, r)
	case r.URL.Path == "/api/terminal/input" && r.Method == "POST":
		h.terminalInput(w, r)
	case r.URL.Path == "/api/workspace" && r.Method == "GET":
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		value, err := h.Service.workspaceDetail(ctx, r.URL.Query().Get("task"), r.URL.Query().Get("generation"))
		if err != nil {
			apiError(w, 409, err.Error())
			return
		}
		respond(w, 200, value)
	case r.URL.Path == "/api/workspace/open" && r.Method == "POST":
		h.openWorkspace(w, r)
	case r.URL.Path == "/api/actions" && r.Method == "POST":
		h.action(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/questions/") && r.Method == "GET":
		h.questionImage(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/reviews/") && r.Method == "GET":
		h.reviewImage(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/tasks/") && r.Method == "GET":
		h.task(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/"):
		apiError(w, 404, "Unknown endpoint or method")
	case (r.Method == "GET" || r.Method == "HEAD") && h.Assets != nil:
		if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/assets/") && r.URL.Path != "/favicon.svg" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/" {
			data, err := fs.ReadFile(h.Assets, "index.html")
			if err != nil {
				http.Error(w, "Board assets unavailable", 503)
				return
			}
			var nonceBytes [24]byte
			if _, err := rand.Read(nonceBytes[:]); err != nil {
				http.Error(w, "Board could not start", 500)
				return
			}
			nonce := base64.RawStdEncoding.EncodeToString(nonceBytes[:])
			w.Header().Set("Content-Security-Policy", strings.Replace(w.Header().Get("Content-Security-Policy"), "style-src 'self'", "style-src 'self' 'nonce-"+nonce+"'", 1))
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if r.Method == "GET" {
				_, _ = io.WriteString(w, strings.Replace(string(data), "<head>", `<head><meta name="cfo-style-nonce" content="`+nonce+`">`, 1))
			}
			return
		}
		http.FileServer(http.FS(h.Assets)).ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func respond[T interface{}](w http.ResponseWriter, status int, value T) {
	data, err := json.Marshal(value)
	if err != nil {
		apiError(w, 500, "Response encoding failed")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
func apiError(w http.ResponseWriter, status int, message string) {
	respond(w, status, struct {
		Error string `json:"error"`
	}{bounded(message, 1500)})
}

func (h *HTTP) stream(w http.ResponseWriter, r *http.Request) {
	select {
	case h.streams <- struct{}{}:
		defer func() { <-h.streams }()
	default:
		apiError(w, 429, "Too many event streams")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, 500, "Streaming unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, unsubscribe := h.Service.subscribe()
	defer unsubscribe()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		snapshot, err := h.Service.Snapshot()
		if err != nil {
			return
		}
		data, err := json.Marshal(snapshot)
		if err != nil {
			return
		}
		controller := http.NewResponseController(w)
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprintf(w, "id: %s:%d\nevent: snapshot\ndata: %s\n\n", snapshot.Instance, snapshot.Revision, data); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-h.Service.done:
			return
		case <-ch:
		case <-ping.C:
		}
	}
}

func (h *HTTP) action(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		apiError(w, 415, "JSON required")
		return
	}
	var input struct {
		ID         string `json:"id"`
		Kind       string `json:"kind"`
		TaskID     string `json:"task_id"`
		Generation string `json:"generation"`
		Text       string `json:"text"`
		File       string `json:"file"`
		Line       int    `json:"line"`
		EndLine    int    `json:"end_line"`
		Side       string `json:"side"`
		Head       string `json:"head"`
		Revision   string `json:"revision"`
		DiffID     string `json:"diff_id"`
		QuestionID string `json:"question_id"`
		ReviewID   string `json:"review_id"`
		RunID      string `json:"run_id"`
		AnswerKind string `json:"answer_kind"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		apiError(w, 400, "Invalid action JSON")
		return
	}
	var extra json.RawMessage
	if decoder.Decode(&extra) != io.EOF {
		apiError(w, 400, "Only one action is accepted")
		return
	}
	if input.Generation == "" {
		apiError(w, 409, "Recipient identity is required; refresh the board")
		return
	}
	if input.File != "" && !SafeFilePath(input.File) {
		apiError(w, 400, "Unsafe file path")
		return
	}
	a := Action{ID: input.ID, Kind: input.Kind, TaskID: input.TaskID, Generation: input.Generation, Text: input.Text, File: input.File, Line: input.Line, EndLine: input.EndLine, Side: input.Side, Head: input.Head, Revision: input.Revision, DiffID: input.DiffID, QuestionID: input.QuestionID, ReviewID: input.ReviewID, RunID: input.RunID, AnswerKind: input.AnswerKind}
	var err error
	if a.Kind == "review" {
		a, err = h.Service.Store.QueueReview(r.Context(), a, h.Service.Options.CFO)
	} else {
		a, err = h.Service.Store.Queue(a)
	}
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	h.Service.publish(nil)
	respond(w, 202, a)
}

func (h *HTTP) task(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/")
	if len(parts) != 2 || state.ValidTaskID(parts[0]) != nil {
		apiError(w, 400, "Invalid task path")
		return
	}
	meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, parts[0])
	if err != nil {
		apiError(w, 404, "Task metadata unavailable")
		return
	}
	if parts[1] == "activity" {
		lines, err := statusTail(h.Service.Store.Home.State, meta.ID)
		if err != nil {
			apiError(w, 503, err.Error())
			return
		}
		respond(w, 200, lines)
		return
	}
	if parts[1] != "files" && parts[1] != "diff" && parts[1] != "history" {
		apiError(w, 404, "Unknown task detail")
		return
	}
	rev, path := r.URL.Query().Get("revision"), r.URL.Query().Get("path")
	if len(rev) > 64 || len(path) > 4096 {
		apiError(w, 400, "Preview parameter exceeds its limit")
		return
	}
	key := meta.ID + "\x00" + meta.SpawnGen + "\x00" + parts[1] + "\x00" + rev + "\x00" + path
	h.mu.Lock()
	cached, ok := h.cache[key]
	h.mu.Unlock()
	if ok && time.Since(cached.at) < 3*time.Second {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(cached.body)
		return
	}
	select {
	case h.gitSlots <- struct{}{}:
		defer func() { <-h.gitSlots }()
	default:
		apiError(w, 503, "Preview is busy; retry shortly")
		return
	}
	var data []byte
	preview := h.Service.previewGit(meta)
	switch parts[1] {
	case "files":
		value, e := preview.Files(r.Context(), meta.Worktree, rev)
		err = e
		if err == nil {
			data, err = json.Marshal(value)
		}
	case "diff":
		value, e := preview.Diff(r.Context(), meta.Worktree, rev, path)
		err = e
		if err == nil {
			data, err = json.Marshal(value)
		}
	case "history":
		value, e := h.Service.Git.History(r.Context(), meta.Worktree)
		err = e
		if err == nil {
			data, err = json.Marshal(value)
		}
	}
	if err != nil {
		apiError(w, 422, err.Error())
		return
	}
	h.mu.Lock()
	if len(h.cache) >= 8 {
		oldest := ""
		for k, v := range h.cache {
			if oldest == "" || v.at.Before(h.cache[oldest].at) {
				oldest = k
			}
		}
		delete(h.cache, oldest)
	}
	h.cache[key] = cachedResponse{data, time.Now()}
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func statusTail(dir, id string) ([]string, error) {
	f, err := os.Open(filepath.Join(dir, id+".status"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := max(int64(0), info.Size()-(64<<10))
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(redact(string(data))), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	return lines, nil
}

var tokenPattern = regexp.MustCompile(`(?i)(bearer\s+|(?:api[_-]?key|token|password|secret)\s*[=:]\s*["']?)[^\s"']+`)
var urlCredential = regexp.MustCompile(`(?i)(https?://)[^\s/@]+:[^\s/@]+@`)

func redact(text string) string {
	return urlCredential.ReplaceAllString(tokenPattern.ReplaceAllString(text, "${1}[redacted]"), "${1}[redacted]@")
}
