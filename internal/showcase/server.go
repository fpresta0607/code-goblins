package showcase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RuntimeInfo locates the one background server shared by all sessions.
type RuntimeInfo struct {
	PID     int       `json:"pid"`
	Port    int       `json:"port"`
	Started time.Time `json:"started"`
}

// RuntimeDir holds server.json. SHOWCASE_AXI_HOME overrides the default
// per-user cache location; tests set it to a temp dir.
func RuntimeDir() (string, error) {
	if dir := os.Getenv("SHOWCASE_AXI_HOME"); dir != "" {
		return dir, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("showcase: no cache dir: %w", err)
	}
	return filepath.Join(cache, "showcase-axi"), nil
}

func runtimePath(dir string) string {
	return filepath.Join(dir, "server.json")
}

// ReadRuntime returns the recorded server endpoint, or an error satisfying
// errors.Is(err, fs.ErrNotExist) when no server was started.
func ReadRuntime(dir string) (RuntimeInfo, error) {
	var info RuntimeInfo
	data, err := os.ReadFile(runtimePath(dir))
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, fmt.Errorf("showcase: corrupt %s: %w", runtimePath(dir), err)
	}
	return info, nil
}

// Server serves review pages and receives feedback for every registered
// session. Session state lives on disk; the server only maps session ids to
// artifact paths, so a restart loses nothing a poll would deliver.
type Server struct {
	mu       sync.Mutex
	sessions map[string]string
	runtime  string
	listener net.Listener
	http     *http.Server
}

// NewServer returns a server that records its endpoint under runtimeDir.
func NewServer(runtimeDir string) *Server {
	return &Server{sessions: map[string]string{}, runtime: runtimeDir}
}

// Handler routes both the browser UI and the CLI's registration calls.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /api/register", s.handleRegister)
	mux.HandleFunc("POST /api/stop", s.handleStop)
	mux.HandleFunc("GET /s/{id}/{$}", s.handlePage)
	mux.HandleFunc("GET /s/{id}/raw", s.handleRaw)
	mux.HandleFunc("GET /s/{id}/state", s.handleState)
	mux.HandleFunc("POST /s/{id}/feedback", s.handleFeedback)
	mux.HandleFunc("POST /s/{id}/end", s.handleEnd)
	mux.HandleFunc("GET /s/{id}/{path...}", s.handleAsset)
	return mux
}

// Serve binds 127.0.0.1 on a free port, records the endpoint for the CLI,
// and serves until stopped. It blocks.
func (s *Server) Serve() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.listener = listener
	if err := os.MkdirAll(s.runtime, 0o755); err != nil {
		return err
	}
	info := RuntimeInfo{PID: os.Getpid(), Port: listener.Addr().(*net.TCPAddr).Port, Started: time.Now()}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := os.WriteFile(runtimePath(s.runtime), data, 0o644); err != nil {
		return err
	}
	defer os.Remove(runtimePath(s.runtime))

	s.http = &http.Server{Handler: s.Handler()}
	err = s.http.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops Serve; the stop endpoint uses it.
func (s *Server) Shutdown() {
	if s.http != nil {
		s.http.Shutdown(context.Background())
	}
}

func (s *Server) artifact(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, ok := s.sessions[id]
	return artifact, ok
}

func (s *Server) register(id, artifact string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = artifact
}

// allowMutation accepts browser requests whose Origin is the loopback
// address the server binds (localhost or 127.0.0.1 on the bound port) and
// non-browser clients (the CLI) that send none, while refusing cross-origin
// side-effect requests from sandboxed or foreign pages. The Origin is
// compared to the local bind address rather than the client-controlled Host
// header so a DNS-rebinding page cannot spoof same-origin.
func (s *Server) allowMutation(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if !strings.EqualFold(u.Scheme, "http") {
		return false
	}
	host := u.Hostname()
	if !strings.EqualFold(host, "127.0.0.1") && !strings.EqualFold(host, "localhost") {
		return false
	}
	local := requestLocalPort(r)
	return local != 0 && u.Port() == strconv.Itoa(local)
}

func requestLocalPort(r *http.Request) int {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return 0
	}
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.Port
	}
	return 0
}

type registerRequest struct {
	Path   string `json:"path"`
	Reopen bool   `json:"reopen"`
}

type registerResponse struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Kind Kind   `json:"kind"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	var request registerRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "bad register body", http.StatusBadRequest)
		return
	}
	artifact, err := filepath.Abs(request.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(artifact); err != nil {
		http.Error(w, fmt.Sprintf("artifact: %v", err), http.StatusNotFound)
		return
	}
	kind, err := DetectFile(artifact)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	session, err := Open(artifact, kind, request.Reopen)
	if errors.Is(err, ErrEndedByUser) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": ErrEndedByUser.Error()})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.register(session.ID, artifact)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(registerResponse{ID: session.ID, URL: "/s/" + session.ID + "/", Kind: kind})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.Shutdown()
	}()
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	artifact, ok := s.artifact(r.PathValue("id"))
	if !ok {
		http.Error(w, "unknown session; rerun showcase-axi <file>", http.StatusNotFound)
		return
	}
	kind, err := DetectFile(artifact)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	content, toc, _, err := buildContent(kind, artifact, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = renderPage(w, pageData{
		Title:   filepath.Base(artifact),
		Kind:    kind,
		TOC:     toc,
		Content: htmltemplate.HTML(content),
		Device:  kind == KindHTML,
		Mermaid: kind == KindMermaid,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "showcase: render %s: %v\n", artifact, err)
	}
}

func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	artifact, ok := s.artifact(r.PathValue("id"))
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "null")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeFile(w, r, artifact)
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	artifact, ok := s.artifact(r.PathValue("id"))
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	dir := filepath.Dir(artifact)
	target := filepath.Clean(filepath.Join(dir, filepath.FromSlash(r.PathValue("path"))))
	if target != dir && !strings.HasPrefix(target, dir+string(filepath.Separator)) {
		http.Error(w, "asset escapes the artifact directory", http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "null")
	http.ServeFile(w, r, target)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	artifact, ok := s.artifact(r.PathValue("id"))
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	session, err := Load(artifact)
	if errors.Is(err, fs.ErrNotExist) {
		session = &Session{ID: r.PathValue("id"), Artifact: artifact}
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	artifact, ok := s.artifact(r.PathValue("id"))
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	var feedback Feedback
	if err := json.NewDecoder(r.Body).Decode(&feedback); err != nil {
		http.Error(w, "bad feedback body", http.StatusBadRequest)
		return
	}
	feedback.Text = strings.TrimSpace(feedback.Text)
	if feedback.Text == "" {
		http.Error(w, "feedback text is empty", http.StatusBadRequest)
		return
	}
	switch feedback.Type {
	case "message", "annotation", "selection":
	default:
		feedback.Type = "message"
	}
	if err := AppendFeedback(artifact, feedback); errors.Is(err, ErrEndedByUser) {
		http.Error(w, ErrEndedByUser.Error(), http.StatusConflict)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEnd(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	artifact, ok := s.artifact(r.PathValue("id"))
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	if err := End(artifact, "user"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
