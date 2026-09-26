package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The CFO delivers a document from anywhere it can read. The board serves a
// copy that outlives the original, opens a PDF in the browser, downloads it
// on request, and never sees where it came from or its digest; opening it
// closes the item with how it was closed.
func TestTheCFODeliversADocumentTheBoardServesAsACopy(t *testing.T) {
	store, h := testStore(t)
	_, identity, _, cfo := primaryFixture(t, store)
	source := filepath.Join(t.TempDir(), "setbacks 1204 Oak St.pdf")
	data := []byte("%PDF-1.7\n1 0 obj << >> endobj\n%%EOF\n")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 2 {
		if err := DeliverDocument(ctx, h, cfo.Terminals, "", "setbacks-1204-oak", "Setbacks and envelope, 1204 Oak St", source, ""); err != nil {
			t.Fatalf("deliver (and an unchanged redeliver): %v", err)
		}
	}
	if err := os.WriteFile(source, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DeliverDocument(ctx, h, cfo.Terminals, "", "setbacks-1204-oak", "Setbacks and envelope, 1204 Oak St", source, ""); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("a redeliver with other content = %v, want refused", err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}

	snapshot, err := (&Service{Store: store}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	published, _ := json.Marshal(snapshot)
	document := snapshot.Reviews[0].Document
	if document == nil || document.Name != "setbacks 1204 Oak St.pdf" || document.Size != int64(len(data)) || document.Kind != "application/pdf" || document.Sum != "" || bytes.Contains(published, []byte(store.Snapshot().Reviews[0].Document.Sum)) || bytes.Contains(published, []byte(filepath.Dir(source))) {
		t.Fatalf("the board saw %+v, want the name, size and type without the digest or the source", document)
	}
	handler := NewHTTP(&Service{Store: store}, "board.local", nil)
	get := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local"+path, nil))
		return response
	}
	opened := get("/api/reviews/setbacks-1204-oak/document")
	if opened.Code != 200 || opened.Header().Get("Content-Type") != "application/pdf" || !strings.HasPrefix(opened.Header().Get("Content-Disposition"), "inline") || opened.Header().Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(opened.Body.Bytes(), data) {
		t.Fatalf("open = %d %v, want the copy inline as a PDF", opened.Code, opened.Header())
	}
	downloaded := get("/api/reviews/setbacks-1204-oak/document?download=1")
	if downloaded.Code != 200 || !strings.HasPrefix(downloaded.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(downloaded.Header().Get("Content-Disposition"), "setbacks 1204 Oak St.pdf") || !bytes.Equal(downloaded.Body.Bytes(), data) {
		t.Fatalf("download = %d %v, want the copy as an attachment named after the file", downloaded.Code, downloaded.Header())
	}
	if unknown := get("/api/reviews/no-such-document/document"); unknown.Code != 404 {
		t.Fatalf("an unknown document = %d, want 404", unknown.Code)
	}

	s := &Service{Store: store}
	if _, err := store.Queue(Action{ID: "open-1", Kind: "review_clear", Generation: identity, ReviewID: "setbacks-1204-oak", Text: "Opened"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(ctx, s.execute); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "cleared" || got.Reason != "Opened" {
		t.Fatalf("after he opened it the item = %+v, want it cleared as Opened", got)
	}
}

// A document a browser could run on the board's origin, or one whose type is
// not a PDF or a raster image, is only ever downloaded.
func TestADocumentABrowserCouldRunIsOnlyEverDownloaded(t *testing.T) {
	for name, content := range map[string]string{
		"page.html":  "<!doctype html><script>alert(1)</script>",
		"notes.svg":  `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"sheet.csv":  "parcel,setback\n1204,25\n",
		"report.pdf": "<html><script>alert(1)</script>",
	} {
		t.Run(name, func(t *testing.T) {
			store, h := testStore(t)
			_, _, _, cfo := primaryFixture(t, store)
			source := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := DeliverDocument(context.Background(), h, cfo.Terminals, "", "document-under-test", "Look at this", source, ""); err != nil {
				t.Fatal(err)
			}
			if err := store.ingestReviews(); err != nil {
				t.Fatal(err)
			}

			response := httptest.NewRecorder()
			NewHTTP(&Service{Store: store}, "board.local", nil).ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/reviews/document-under-test/document", nil))

			if response.Code != 200 || response.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment") || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("%s = %d %v, want it only downloaded", name, response.Code, response.Header())
			}
		})
	}
}

// A goblin delivers only from its own worktree, task scratch or data
// directory, and no document may be larger than the cap or name a link that
// carries a query.
func TestADocumentIsRefusedOutsideItsReportersFoldersOrLimits(t *testing.T) {
	store, h := testStore(t)
	meta, _, _, goblin := goblinFixture(t, store)
	_, _, _, cfo := primaryFixture(t, store)
	ctx := context.Background()
	outside := filepath.Join(t.TempDir(), "elsewhere.pdf")
	inside := filepath.Join(meta.Worktree, "plan.pdf")
	huge := filepath.Join(t.TempDir(), "huge.pdf")
	for _, path := range []string{outside, inside, huge} {
		if err := os.WriteFile(path, []byte("%PDF-1.7\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Truncate(huge, maxDocument+1); err != nil {
		t.Fatal(err)
	}

	if err := DeliverDocument(ctx, h, goblin.Terminals, meta.ID, "goblin-outside", "Plan", outside, ""); err == nil || !strings.Contains(err.Error(), "inside its worktree") {
		t.Errorf("a goblin's document outside its folders = %v, want refused", err)
	}
	if err := DeliverDocument(ctx, h, goblin.Terminals, meta.ID, "goblin-inside", "Plan", inside, ""); err != nil {
		t.Errorf("a goblin's document inside its worktree = %v, want it delivered", err)
	}
	if err := DeliverDocument(ctx, h, cfo.Terminals, "", "cfo-too-large", "Plan", huge, ""); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("a document over the cap = %v, want refused", err)
	}
	if err := DeliverDocument(ctx, h, cfo.Terminals, "", "cfo-signed-link", "Plan", outside, "https://files.example.com/plan.pdf?X-Amz-Signature=abc"); err == nil || !strings.Contains(err.Error(), "query") {
		t.Errorf("a document whose link carries a query = %v, want refused", err)
	}
	if err := DeliverDocument(ctx, h, cfo.Terminals, "", "cfo-hosted-link", "Plan", outside, "https://files.example.com/plan.pdf"); err != nil {
		t.Errorf("a document with a plain hosted link = %v, want it delivered", err)
	}
}

// Clearing an item carries text only to say its document was opened or
// downloaded, so the board cannot put words in a clear.
func TestAClearCarriesOnlyHowTheDocumentWasOpened(t *testing.T) {
	store, _ := testStore(t)
	_, identity, _, _ := primaryFixture(t, store)
	for n, text := range []string{"Opened", "Downloaded", "", "Approved by the Overlord"} {
		id := "plan-document-" + strconv.Itoa(n)
		now := time.Now().UTC()
		if err := store.acceptReview(Review{ID: id, Identity: identity, Title: "Plan", State: "open", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		_, err := store.Queue(Action{ID: "clear-" + id, Kind: "review_clear", Generation: identity, ReviewID: id, Text: text})
		if allowed := text != "Approved by the Overlord"; (err == nil) != allowed {
			t.Errorf("a clear with text %q = %v, want allowed %v", text, err, allowed)
		}
	}
}
