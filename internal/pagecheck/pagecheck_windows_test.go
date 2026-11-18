package pagecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
)

// reviewServer stands in for lavish-axi's server: a review window holding
// the page in a sandboxed frame, whose stand-in layout gate reports how far
// the page scrolls sideways at its width, recorded as lavish-axi records it.
// It measures once Chrome has given the frame a width: a CI runner's Chrome
// can lay the frame out after the page's first timer runs.
type reviewServer struct {
	page     string
	mu       sync.Mutex
	revision int
	warnings map[string]Warning
	views    []int
}

func (s *reviewServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/session/k7":
		fmt.Fprint(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"></head><body style="margin:0"><header style="height:60px">Scrawl</header>`+
			`<iframe src="/artifact/k7/index.html?token=1" sandbox="allow-scripts" style="display:block;width:100vw;height:calc(100vh - 60px);border:0"></iframe>`+
			`<script>addEventListener("message", (event) => fetch("/api/k7/layout-diagnostics", { method: "POST", body: JSON.stringify(event.data) }));</script></body></html>`)
	case r.URL.Path == "/artifact/k7/index.html":
		fmt.Fprint(w, s.page+`<script>let tries = 0; const measure = () => innerWidth === 0 && ++tries < 200 ? setTimeout(measure, 50) : parent.postMessage({ overflow: document.documentElement.scrollWidth - innerWidth, width: innerWidth }, "*"); setTimeout(measure, 100);</script>`)
	case r.URL.Path == "/api/k7/layout-diagnostics" && r.Method == http.MethodPost:
		var measured struct{ Overflow, Width int }
		if err := json.NewDecoder(r.Body).Decode(&measured); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		class := "desktop"
		if measured.Width < 600 {
			class = "mobile"
		}
		s.revision++
		s.views = append(s.views, measured.Width)
		delete(s.warnings, class)
		if measured.Overflow > 0 {
			s.warnings[class] = Warning{Rule: "page-horizontal-overflow", Title: "Page scrolls sideways", Explanation: fmt.Sprintf("The page is %dpx wider than the %dpx viewport.", measured.Overflow, measured.Width), Selector: "html", ViewportClass: class, Status: "open"}
		}
	case r.URL.Path == "/api/k7/layout-warnings":
		s.mu.Lock()
		defer s.mu.Unlock()
		found := recorded{Revision: s.revision, Warnings: []Warning{}}
		for _, class := range []string{"desktop", "mobile", "compact", "wide"} {
			if warning, ok := s.warnings[class]; ok {
				found.Warnings = append(found.Warnings, warning)
			}
		}
		_ = json.NewEncoder(w).Encode(found)
	default:
		http.NotFound(w, r)
	}
}

func TestACheckDrawsThePageHiddenAtBothWidthsAndListsWhatTheGateFound(t *testing.T) {
	executable, err := browserlogin.Executable()
	if err != nil {
		t.Fatal(err)
	}
	previous := settleQuiet
	settleQuiet = 500 * time.Millisecond
	t.Cleanup(func() { settleQuiet = previous })
	tall := `<div style="height:3000px">A long page</div>`
	short := `<p>Short words that wrap.</p>`
	for _, c := range []struct {
		name, page string
		inbox      map[string]Warning
		want       []string
	}{
		{"a page whose line never wraps", `<p style="white-space:nowrap;font-size:40px">` + strings.Repeat("words that run past the edge ", 40) + `</p>` + tall, nil, []string{"desktop", "mobile"}},
		{"a page that fits", short + tall, nil, nil},
		{"warnings his inbox already holds, one still open and one resolved", short, map[string]Warning{
			"compact": {Rule: "clipped-text", Title: "Text cut off by its container", ViewportClass: "compact", Status: "open"},
			"wide":    {Rule: "clipped-text", Title: "Text cut off by its container", ViewportClass: "wide", Status: "resolved"},
		}, []string{"compact"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			review := &reviewServer{page: `<!doctype html><html><body style="margin:0">` + c.page + `</body></html>`, warnings: map[string]Warning{}}
			for class, warning := range c.inbox {
				review.warnings[class] = warning
			}
			server := httptest.NewServer(review)
			defer server.Close()
			out := filepath.Join(t.TempDir(), "check")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			// Act
			report, err := Check(ctx, executable, server.URL+"/session/k7", out)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			var classes []string
			for _, warning := range report.Warnings {
				classes = append(classes, warning.ViewportClass)
			}
			if fmt.Sprint(classes) != fmt.Sprint(c.want) {
				t.Errorf("warnings at %v, want %v: %+v", classes, c.want, report.Warnings)
			}
			review.mu.Lock()
			measured := review.views
			review.mu.Unlock()
			if fmt.Sprint(measured) != "[1920 390]" {
				t.Errorf("the gate measured widths %v, want the desktop's 1920, then the phone's 390", measured)
			}
			for i, view := range Views {
				picture := report.Pictures[i]
				data, err := os.ReadFile(picture.Path)
				if err != nil {
					t.Fatal(err)
				}
				drawn, err := png.DecodeConfig(bytes.NewReader(data))
				if err != nil {
					t.Fatalf("%s is no PNG: %v", picture.Path, err)
				}
				wantHeight := view.Height - 60
				if c.page != short {
					wantHeight = 3000
				}
				if drawn.Width != view.Width || drawn.Height < wantHeight {
					t.Errorf("the %s picture is %dx%d, want %d wide and at least %d high", view.Name, drawn.Width, drawn.Height, view.Width, wantHeight)
				}
			}
		})
	}
}
