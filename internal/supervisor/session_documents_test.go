package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func documentPDF() []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents 4 0 R >>", "<< /Length 0 >>\nstream\n\nendstream"}
	var file bytes.Buffer
	file.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for index, object := range objects {
		offsets = append(offsets, file.Len())
		fmt.Fprintf(&file, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := file.Len()
	fmt.Fprintf(&file, "xref\n0 5\n0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&file, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&file, "trailer\n<< /Size 5 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return file.Bytes()
}

func documentBoard(t *testing.T) (*HTTP, state.TaskMeta, string) {
	t.Helper()
	store, taskHome := testStore(t)
	profile := t.TempDir()
	meta := codexGoblin(t, taskHome.State, profile, "document-task", "document-generation")
	if err := os.MkdirAll(meta.Worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	board := NewHTTP(&Service{Store: store, Instance: "document-token", Options: Options{Tree: &fleettree.Reader{Home: profile}}}, "board.local", nil)
	return board, meta, fleettree.SessionTranscript(profile, "codex", meta.ID+"-session")
}

func documentReference(t *testing.T, transcript, target string) {
	t.Helper()
	entry := struct {
		Type    string `json:"type"`
		Payload struct {
			Type string `json:"type"`
			Item struct {
				Type    string `json:"type"`
				ID      string `json:"id"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"item"`
		} `json:"payload"`
	}{Type: "event_msg"}
	entry.Payload.Type = "item_completed"
	entry.Payload.Item.Type, entry.Payload.Item.ID = "AgentMessage", target
	entry.Payload.Item.Content = append(entry.Payload.Item.Content, struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{"Text", "[Retail candidate PDF](" + target + "): report preserved."})
	file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = json.NewEncoder(file).Encode(entry)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("write reference: %v, %v", err, closeErr)
	}
}

func requestDocument(board *HTTP, meta state.TaskMeta, target, token string) *httptest.ResponseRecorder {
	query := url.Values{"task": {meta.ID}, "generation": {meta.SpawnGen}, "document": {target}}
	request := httptest.NewRequest("GET", "http://"+board.Host+"/api/messages?"+query.Encode(), nil)
	request.Header.Set("X-CEO-Token", token)
	response := httptest.NewRecorder()
	board.ServeHTTP(response, request)
	return response
}

func TestSessionDocumentsServeOnlyReferencedTaskPDFs(t *testing.T) {
	for _, spelling := range []string{"native", "forward", "encoded"} {
		t.Run(spelling, func(t *testing.T) {
			board, meta, transcript := documentBoard(t)
			file := filepath.Join(meta.Worktree, "Retail candidate (approved).pdf")
			pdf := documentPDF()
			if err := os.WriteFile(file, pdf, 0o600); err != nil {
				t.Fatal(err)
			}
			target := file
			if spelling == "forward" {
				target = filepath.ToSlash(file)
			} else if spelling == "encoded" {
				target = strings.ReplaceAll(file, " ", "%20")
			}
			documentReference(t, transcript, target)

			response := requestDocument(board, meta, target, "document-token")

			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), pdf) || response.Header().Get("Content-Type") != "application/pdf" {
				t.Fatalf("PDF action = %d %s %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("PDF response lacks private non-sniffable headers", response.Header())
			}
		})
	}
}

func TestSessionDocumentsAllowOnlyRegisteredTaskRoots(t *testing.T) {
	board, meta, transcript := documentBoard(t)
	meta.TaskTmp, meta.Scratch = t.TempDir(), t.TempDir()
	meta.Extras = []string{t.TempDir()}
	if err := state.WriteTaskMeta(board.Service.Store.Home.State, meta); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, root string }{
		{"worktree", meta.Worktree},
		{"extra worktree", meta.Extras[0]},
		{"task temporary directory", meta.TaskTmp},
		{"scratch", meta.Scratch},
		{"task data", filepath.Join(board.Service.Store.Home.Data, meta.ID)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.MkdirAll(test.root, 0o700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(test.root, "report.pdf")
			if err := os.WriteFile(file, documentPDF(), 0o600); err != nil {
				t.Fatal(err)
			}
			documentReference(t, transcript, file)

			response := requestDocument(board, meta, file, "document-token")

			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), documentPDF()) {
				t.Fatalf("registered document root refused: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSessionDocumentsReachTheFirstOf10001Messages(t *testing.T) {
	for _, kind := range []string{"UserMessage", "AgentMessage"} {
		t.Run(kind, func(t *testing.T) {
			board, meta, transcript := documentBoard(t)
			file := filepath.Join(meta.Worktree, "report.pdf")
			pdf := documentPDF()
			if err := os.WriteFile(file, pdf, 0o600); err != nil {
				t.Fatal(err)
			}
			var entries bytes.Buffer
			for index := range 10001 {
				messageKind, text := "UserMessage", "Later message"
				if index == 0 {
					messageKind, text = kind, "[Old report]("+file+")"
				}
				encoded, err := json.Marshal(text)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&entries, "{\"type\":\"event_msg\",\"payload\":{\"type\":\"item_completed\",\"item\":{\"type\":%q,\"id\":\"message-%d\",\"content\":[{\"type\":\"Text\",\"text\":%s}]}}}\n", messageKind, index, encoded)
			}
			if err := os.WriteFile(transcript, entries.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}

			response := requestDocument(board, meta, file, "document-token")

			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), pdf) {
				t.Fatalf("old document reference refused: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSessionDocumentsRefuseCancelledReferenceLookups(t *testing.T) {
	for _, name := range []string{"cancelled", "deadline exceeded"} {
		t.Run(name, func(t *testing.T) {
			board, meta, transcript := documentBoard(t)
			file := filepath.Join(meta.Worktree, "report.pdf")
			if err := os.WriteFile(file, documentPDF(), 0o600); err != nil {
				t.Fatal(err)
			}
			documentReference(t, transcript, file)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if name == "deadline exceeded" {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
			}
			query := url.Values{"task": {meta.ID}, "generation": {meta.SpawnGen}, "document": {file}}
			request := httptest.NewRequest("GET", "http://"+board.Host+"/api/messages?"+query.Encode(), nil).WithContext(ctx)
			request.Header.Set("X-CEO-Token", "document-token")
			response := httptest.NewRecorder()

			board.ServeHTTP(response, request)

			if response.Code != http.StatusForbidden || response.Body.String() != "{\"error\":\""+UNAVAILABLE_DOCUMENT+"\"}" {
				t.Fatalf("cancelled PDF lookup = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSessionDocumentsKeepRangeReadsPrivate(t *testing.T) {
	board, meta, transcript := documentBoard(t)
	file := filepath.Join(meta.Worktree, "report.pdf")
	if err := os.WriteFile(file, documentPDF(), 0o600); err != nil {
		t.Fatal(err)
	}
	documentReference(t, transcript, file)
	query := url.Values{"task": {meta.ID}, "generation": {meta.SpawnGen}, "document": {file}}
	request := httptest.NewRequest("GET", "http://"+board.Host+"/api/messages?"+query.Encode(), nil)
	request.Header.Set("X-CEO-Token", "document-token")
	request.Header.Set("Range", "bytes=0-7")
	response := httptest.NewRecorder()

	board.ServeHTTP(response, request)

	if response.Code != http.StatusPartialContent || response.Body.String() != "%PDF-1.4" || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("ranged PDF = %d %s %v", response.Code, response.Body.String(), response.Header())
	}
}

func TestSessionDocumentsRequireTheReferenceInThisConversation(t *testing.T) {
	board, meta, _ := documentBoard(t)
	other := codexGoblin(t, board.Service.Store.Home.State, board.Service.Options.Tree.Home, "separate-task", "other-generation")
	file := filepath.Join(meta.Worktree, "report.pdf")
	if err := os.WriteFile(file, documentPDF(), 0o600); err != nil {
		t.Fatal(err)
	}
	documentReference(t, fleettree.SessionTranscript(board.Service.Options.Tree.Home, "codex", other.ID+"-session"), file)

	response := requestDocument(board, meta, file, "document-token")

	if response.Code != http.StatusForbidden {
		t.Fatalf("another conversation authorized a PDF: %d %s", response.Code, response.Body.String())
	}
}

func TestSessionDocumentsRejectQueryTokensAndForeignOrigins(t *testing.T) {
	board, meta, transcript := documentBoard(t)
	file := filepath.Join(meta.Worktree, "report.pdf")
	if err := os.WriteFile(file, documentPDF(), 0o600); err != nil {
		t.Fatal(err)
	}
	documentReference(t, transcript, file)
	for _, name := range []string{"query token", "foreign origin", "CEO access"} {
		t.Run(name, func(t *testing.T) {
			query := url.Values{"task": {meta.ID}, "generation": {meta.SpawnGen}, "document": {file}}
			if name == "query token" {
				query.Set("token", "document-token")
			} else if name == "CEO access" {
				query.Set("ceo", "native-terminal")
			}
			request := httptest.NewRequest("GET", "http://"+board.Host+"/api/messages?"+query.Encode(), nil)
			if name != "query token" {
				request.Header.Set("X-CEO-Token", "document-token")
			}
			if name == "foreign origin" {
				request.Header.Set("Origin", "http://untrusted.example")
			}
			response := httptest.NewRecorder()

			board.ServeHTTP(response, request)

			if response.Code != http.StatusForbidden {
				t.Fatalf("unsafe PDF authentication = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSessionDocumentPathsRefuseSharesAndDevicesBeforeOpening(t *testing.T) {
	for _, target := range []string{`\\server\share\candidate.pdf`, "//server/share/candidate.pdf", `/\server\share\candidate.pdf`, `\/server/share/candidate.pdf`, `\\.\C:\candidate.pdf`} {
		if isDocumentPath(target) {
			t.Errorf("share or device path %q passed the document path check", target)
		}
	}
}

func TestSessionDocumentsRefuseInvalidAccessWithoutDisclosingPaths(t *testing.T) {
	for _, name := range []string{"no token", "stale generation", "cross task", "unreferenced", "outside root", "traversal", "encoded traversal", "UNC", "UNC slash then backslash", "UNC backslash then slash", "device", "ADS", "not PDF", "wrong extension", "over limit", "missing", "directory", "malformed encoding"} {
		t.Run(name, func(t *testing.T) {
			board, meta, transcript := documentBoard(t)
			file := filepath.Join(meta.Worktree, "candidate.pdf")
			content := documentPDF()
			target, token := file, "document-token"
			switch name {
			case "no token":
				token = ""
			case "stale generation":
				meta.SpawnGen = "old-generation"
			case "cross task":
				other := codexGoblin(t, board.Service.Store.Home.State, board.Service.Options.Tree.Home, "separate-task", "document-generation")
				if err := os.MkdirAll(other.Worktree, 0o700); err != nil {
					t.Fatal(err)
				}
				file = filepath.Join(other.Worktree, "candidate.pdf")
				target = file
			case "outside root":
				file = filepath.Join(t.TempDir(), "private.pdf")
				target = file
			case "traversal":
				target = filepath.Dir(file) + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(meta.Worktree) + string(filepath.Separator) + "candidate.pdf"
			case "encoded traversal":
				target = filepath.Dir(file) + "/%2e%2e/" + filepath.Base(meta.Worktree) + "/candidate.pdf"
			case "UNC":
				target = `\\server\share\candidate.pdf`
			case "UNC slash then backslash":
				target = `/\server\share\candidate.pdf`
			case "UNC backslash then slash":
				target = `\/server/share/candidate.pdf`
			case "device":
				target = `\\?\C:\candidate.pdf`
			case "ADS":
				target = file + ":hidden.pdf"
			case "malformed encoding":
				target = filepath.Join(meta.Worktree, "%zz.pdf")
			case "not PDF":
				content = []byte("<html><script>active content</script></html>")
			case "wrong extension":
				file = filepath.Join(meta.Worktree, "candidate.txt")
				target = file
			}
			if err := os.WriteFile(file, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if name == "over limit" {
				if err := os.Truncate(file, maxDocument+1); err != nil {
					t.Fatal(err)
				}
			}
			if name != "unreferenced" {
				documentReference(t, transcript, target)
			}
			if name == "missing" {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			}
			if name == "directory" {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(file, 0o700); err != nil {
					t.Fatal(err)
				}
			}

			response := requestDocument(board, meta, target, token)

			if response.Code < 400 || bytes.Contains(response.Body.Bytes(), []byte(meta.Worktree)) || bytes.Contains(response.Body.Bytes(), content) {
				t.Fatalf("unsafe PDF action = %d %s", response.Code, response.Body.String())
			}
		})
	}
}
