package browserlogin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

type devToolsBridge struct {
	pipe    *devToolsPipe
	server  *http.Server
	address string
}

func (b *devToolsBridge) listen() error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	b.address = "http://" + listener.Addr().String()
	b.server = &http.Server{Handler: ownerOnlyDevTools(user.User.Sid.String(), http.HandlerFunc(b.serve)), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() { _ = b.server.Serve(listener) }()
	return nil
}

func (b *devToolsBridge) close() {
	if b.server != nil {
		b.server.Close()
	}
	b.pipe.close()
}

func (b *devToolsBridge) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/devtools/browser" {
		b.pipe.serveClient(w, r, "")
		return
	}
	if target, ok := strings.CutPrefix(r.URL.Path, "/devtools/page/"); ok && target != "" {
		b.pipe.serveClient(w, r, target)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	socket := strings.Replace(b.address, "http://", "ws://", 1)
	switch r.URL.Path {
	case "/json/version":
		var version struct{ Product, ProtocolVersion, UserAgent, JSVersion string }
		if err := b.pipe.call(ctx, "Browser.getVersion", nil, &version); err != nil {
			http.Error(w, "the browser did not answer: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"Browser": version.Product, "Protocol-Version": version.ProtocolVersion, "User-Agent": version.UserAgent, "V8-Version": version.JSVersion, "webSocketDebuggerUrl": socket + "/devtools/browser"})
	case "/json", "/json/list":
		var result struct {
			Targets []struct {
				ID               string `json:"targetId"`
				Type, Title, URL string
			} `json:"targetInfos"`
		}
		if err := b.pipe.call(ctx, "Target.getTargets", nil, &result); err != nil {
			http.Error(w, "the browser did not answer: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		targets := make([]map[string]string, 0, len(result.Targets))
		for _, target := range result.Targets {
			targets = append(targets, map[string]string{"id": target.ID, "type": target.Type, "title": target.Title, "url": target.URL, "webSocketDebuggerUrl": socket + "/devtools/page/" + url.PathEscape(target.ID)})
		}
		json.NewEncoder(w).Encode(targets)
	default:
		http.NotFound(w, r)
	}
}
