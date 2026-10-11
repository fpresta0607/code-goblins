package supervisor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	stdhtml "html"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The Overlord, 2026-10-11 about 00:55Z, of the Open and Download card the CFO
// made for a picture of a board fix: "things like that should be
// scrawl...downlaods can be offeried inn scralw if its a generated asset or
// smomehting the overlord wanted saved", then "that way you can send comments
// back to agent for it..". cfo deliver makes a Scrawl page that shows the
// file, presented as a review item the supervisor watches.

// webpPicture is the start of a WebP picture, enough for a type sniff.
var webpPicture = append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), bytes.Repeat([]byte{7}, 64)...)

// opened stands in for lavish-axi opening a page: it records each page and
// gives it an address.
type opened struct{ pages []string }

func (o *opened) open(page string) (string, error) {
	o.pages = append(o.pages, page)
	return pageLink, nil
}

// delivered writes content to name in dir and delivers it as the registered
// CFO, returning the page lavish-axi was asked to open.
func delivered(t *testing.T, store *Store, dir, name string, content []byte, isDownload bool) string {
	t.Helper()
	source := filepath.Join(dir, name)
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}
	pages := &opened{}
	if err := DeliverDocument(store.Home, "", "board-card-text-fits", "The orchestration card, after the fit", source, isDownload, pages.open); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	if len(pages.pages) != 1 {
		t.Fatalf("lavish-axi opened %q, want the one page", pages.pages)
	}
	return pages.pages[0]
}

// A picture the CFO delivers is a Scrawl page that shows it inline, under its
// title, beside its own copy, with no download: the item is that page, which
// the supervisor watches, and the board sees no digest and no source.
func TestADeliveredPictureIsAScrawlPageThatShowsIt(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	_, _, _, cfo := primaryFixture(t, store)
	servePipe(t, store, cfo)
	source := t.TempDir()

	// Act
	page := delivered(t, store, source, "after-card.webp", webpPicture, false)

	// Assert
	item := store.Snapshot().Reviews[0]
	if item.Lavish != pageLink || item.LavishPage != page || filepath.Dir(page) != reviewImageDir(h.State, item) || item.State != "open" || item.Document == nil || item.Document.Kind != "image/webp" || item.Document.Download {
		t.Fatalf("the item = %+v, want the opened page in its own folder, open, showing a WebP picture with no download", item)
	}
	html, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`<title>The orchestration card, after the fit</title>`, `<img src="file/after-card.webp" alt="The orchestration card, after the fit">`, `/design/board-tokens.css`} {
		if !strings.Contains(string(html), part) {
			t.Errorf("the page lacks %s:\n%s", part, html)
		}
	}
	if strings.Contains(string(html), "Download") {
		t.Errorf("the page offers a download of a picture only to look at:\n%s", html)
	}
	if copied, err := os.ReadFile(filepath.Join(filepath.Dir(page), "file", "after-card.webp")); err != nil || !bytes.Equal(copied, webpPicture) {
		t.Errorf("the copy beside the page = %d bytes (%v), want the picture", len(copied), err)
	}
	snapshot, err := (&Service{Store: store}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	published, _ := json.Marshal(snapshot)
	if bytes.Contains(published, []byte(item.Document.Sum)) || bytes.Contains(published, []byte(source)) {
		t.Errorf("the board saw the digest or the source folder: %s", published)
	}
}

// A file he asked to keep or a generated asset is offered to download inside
// its page, and so is one the page cannot show, which needs --download.
func TestADeliveredFilesPageOffersADownloadOnlyWhenAsked(t *testing.T) {
	for name, test := range map[string]struct {
		file       string
		content    []byte
		isDownload bool
		shows      string
		refusal    string
	}{
		"a picture he asked to keep":        {file: "after-card.webp", content: webpPicture, isDownload: true, shows: `<img src="file/after-card.webp"`},
		"a PDF, drawn page by page":         {file: "setbacks.pdf", content: []byte("%PDF-1.7\n1 0 obj << >> endobj\n%%EOF\n"), shows: `data-pdf="` + base64.StdEncoding.EncodeToString([]byte("%PDF-1.7\n1 0 obj << >> endobj\n%%EOF\n")) + `"`},
		"plain text, as text":               {file: "notes.txt", content: []byte("parcel 1204 & 1206 <setbacks>\n"), shows: "<pre>parcel 1204 & 1206 <setbacks>\n</pre>"},
		"a web page, never run in the page": {file: "page.html", content: []byte("<!doctype html><script>alert(1)</script>"), isDownload: true, shows: `<p class="muted">page.html</p>`},
		"a web page with no download":       {file: "page.html", content: []byte("<!doctype html><script>alert(1)</script>"), refusal: "--download"},
		"a zip archive with no download":    {file: "assets.zip", content: []byte("PK\x03\x04\x00\x00\x00\x00"), refusal: "--download"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			_, _, _, cfo := primaryFixture(t, store)
			servePipe(t, store, cfo)
			source := filepath.Join(t.TempDir(), test.file)
			if err := os.WriteFile(source, test.content, 0o600); err != nil {
				t.Fatal(err)
			}
			pages := &opened{}

			// Act
			err := DeliverDocument(store.Home, "", "delivered-file-1", "Look at this", source, test.isDownload, pages.open)

			// Assert
			if test.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), test.refusal) || len(pages.pages) != 0 {
					t.Fatalf("deliver = %v opening %q, want it refused naming %s before any page opens", err, pages.pages, test.refusal)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			html, err := os.ReadFile(pages.pages[0])
			if err != nil {
				t.Fatal(err)
			}
			// An attribute reads back as the browser reads it.
			if !strings.Contains(stdhtml.UnescapeString(string(html)), test.shows) {
				t.Errorf("the page lacks %s:\n%s", test.shows, html)
			}
			if hasDownload := strings.Contains(string(html), `download="`+test.file+`"`); hasDownload != test.isDownload {
				t.Errorf("the page offers a download %v, want %v:\n%s", hasDownload, test.isDownload, html)
			}
			if strings.Contains(string(html), "<script>alert(1)") || strings.Contains(string(html), "<setbacks>") {
				t.Errorf("the page carries the file's own script:\n%s", html)
			}
		})
	}
}

// An unchanged redelivery changes nothing and opens no second page, and a
// redelivery with other content under the same ID is refused.
func TestARedeliveryIsTheSameItemOrRefused(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, _, _, cfo := primaryFixture(t, store)
	servePipe(t, store, cfo)
	dir := t.TempDir()
	delivered(t, store, dir, "after-card.webp", webpPicture, false)
	source := filepath.Join(dir, "after-card.webp")
	pages := &opened{}

	// Act
	same := DeliverDocument(store.Home, "", "board-card-text-fits", "The orchestration card, after the fit", source, false, pages.open)
	if err := os.WriteFile(source, append(webpPicture, 1), 0o600); err != nil {
		t.Fatal(err)
	}
	other := DeliverDocument(store.Home, "", "board-card-text-fits", "The orchestration card, after the fit", source, false, pages.open)

	// Assert
	if same != nil || len(pages.pages) != 0 {
		t.Errorf("an unchanged redelivery = %v opening %q, want nothing new", same, pages.pages)
	}
	if other == nil || !strings.Contains(other.Error(), "already used") {
		t.Errorf("a redelivery with other content = %v, want refused", other)
	}
}

// A goblin delivers only from its own worktree, task scratch or data
// directory, and no file may be larger than the cap.
func TestADeliveryIsRefusedOutsideItsReportersFoldersOrLimits(t *testing.T) {
	store, h := testStore(t)
	meta, _, _, _ := goblinFixture(t, store)
	_, _, _, cfo := primaryFixture(t, store)
	servePipe(t, store, cfo)
	outside := filepath.Join(t.TempDir(), "elsewhere.webp")
	inside := filepath.Join(meta.Worktree, "plan.webp")
	huge := filepath.Join(t.TempDir(), "huge.webp")
	for _, path := range []string{outside, inside, huge} {
		if err := os.WriteFile(path, webpPicture, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Truncate(huge, maxDocument+1); err != nil {
		t.Fatal(err)
	}
	pages := &opened{}

	if err := DeliverDocument(h, meta.ID, "goblin-outside", "Plan", outside, false, pages.open); err == nil || !strings.Contains(err.Error(), "inside its worktree") {
		t.Errorf("a goblin's file outside its folders = %v, want refused", err)
	}
	if err := DeliverDocument(h, meta.ID, "goblin-inside", "Plan", inside, false, pages.open); err != nil {
		t.Errorf("a goblin's file inside its worktree = %v, want it delivered", err)
	}
	if err := DeliverDocument(h, "", "cfo-too-large", "Plan", huge, true, pages.open); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("a file over the cap = %v, want refused", err)
	}
}

// A clear carries no text, so the board cannot put words in it.
func TestAClearCarriesNoText(t *testing.T) {
	store, _ := testStore(t)
	_, identity, _, _ := primaryFixture(t, store)
	for n, text := range []string{"", "Opened", "Approved by the Overlord"} {
		id := "plan-review-" + string(rune('a'+n))
		now := time.Now().UTC()
		if err := store.acceptReview(Review{ID: id, Identity: identity, Title: "Plan", State: "open", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		_, err := store.Queue(Action{ID: "clear-" + id, Kind: "review_clear", Generation: identity, ReviewID: id, Text: text})
		if allowed := text == ""; (err == nil) != allowed {
			t.Errorf("a clear with text %q = %v, want allowed %v", text, err, allowed)
		}
	}
}

// commentOnThePicture is what lavish-axi's poll prints of a comment he wrote
// on the delivered picture without ending the review.
const commentOnThePicture = "session:\n" +
	"  status: feedback\n" +
	"prompts[1]{uid,prompt,selector,tag,text}:\n" +
	"  u3,Make the card a little wider,img,element,The orchestration card\n" +
	"next_step: \"Apply the requested changes.\"\n"

// pollsOnce answers the first poll of page with output, then each later poll
// with nothing once its timeout passes, as a page he wrote on once does.
func pollsOnce(t *testing.T, page, output string) func(context.Context, string, string, time.Duration) (axi.PagePoll, error) {
	t.Helper()
	isTold := false
	return func(ctx context.Context, file, reply string, timeout time.Duration) (axi.PagePoll, error) {
		if file != page {
			t.Errorf("polled %s, want the delivered page %s", file, page)
		}
		if !isTold {
			isTold = true
			return axi.Lavish{Commands: printed(output)}.Poll(ctx, file, reply, timeout)
		}
		select {
		case <-ctx.Done():
			return axi.PagePoll{}, ctx.Err()
		case <-time.After(timeout):
			return axi.Lavish{Commands: printed(scrawlWaiting)}.Poll(ctx, file, reply, timeout)
		}
	}
}

// pageFeedbackOf reads the feedback a review wake's detail says it saved.
func pageFeedbackOf(t *testing.T, detail string) string {
	t.Helper()
	_, saved, found := strings.Cut(detail, "his feedback is in ")
	saved, _, _ = strings.Cut(saved, ", ")
	data, err := os.ReadFile(saved)
	if !found || err != nil {
		t.Fatalf("the wake %q names no saved feedback (%v)", detail, err)
	}
	return string(data)
}

// What he writes on the CFO's delivered picture reaches the CFO as a review
// wake keyed by the item.
func TestACommentOnTheCFOsDeliveredPictureReachesItAsAReviewWake(t *testing.T) {
	// Arrange
	defer func(pause time.Duration) { pagePollPause = pause }(pagePollPause)
	pagePollPause = time.Millisecond
	store, h := testStore(t)
	_, _, _, cfo := primaryFixture(t, store)
	servePipe(t, store, cfo)
	page := delivered(t, store, t.TempDir(), "after-card.webp", webpPicture, false)
	s := &Service{Store: store, Options: Options{PollPage: pollsOnce(t, page, commentOnThePicture)}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Act
	s.watchPages(ctx)
	var wakes []wake.Record
	for deadline := time.Now().Add(10 * time.Second); len(wakes) == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		wakes = reviewWakes(t, h.State, "board-card-text-fits")
	}
	cancel()
	s.pageWork.Wait()

	// Assert
	if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, page) {
		t.Fatalf("review wakes = %+v, want one keyed by the delivered item naming its page", wakes)
	}
	if feedback := pageFeedbackOf(t, wakes[0].Detail); !strings.Contains(feedback, "Make the card a little wider") {
		t.Errorf("the CFO's wake carries %q, want his comment", feedback)
	}
}

// What he writes on a goblin's delivered picture reaches that goblin, in its
// own terminal.
func TestACommentOnAGoblinsDeliveredPictureReachesTheGoblin(t *testing.T) {
	// Arrange
	defer func(pause time.Duration) { pagePollPause = pause }(pagePollPause)
	pagePollPause = time.Millisecond
	store, h := testStore(t)
	meta, _, goblin, _ := goblinFixture(t, store)
	_, _, _, cfo := primaryFixture(t, store)
	servePipe(t, store, cfo)
	source := filepath.Join(meta.Worktree, "after-card.webp")
	if err := os.WriteFile(source, webpPicture, 0o600); err != nil {
		t.Fatal(err)
	}
	pages := &opened{}
	if err := DeliverDocument(h, meta.ID, "task-1-after-card", "The orchestration card, after the fit", source, false, pages.open); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, Options: Options{CFO: cfo, PollPage: pollsOnce(t, pages.pages[0], commentOnThePicture)}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Act
	s.watchPages(ctx)
	typed := goblin.waitForLines(t, 1)
	cancel()
	s.pageWork.Wait()

	// Assert
	if len(typed) != 1 || !strings.Contains(typed[0], "Make the card a little wider") {
		t.Fatalf("the goblin received %q, want his comment on its page", typed)
	}
}

// The Open and Download card's file link is gone with it.
func TestTheBoardServesNoDeliveredFileLink(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, _, _, cfo := primaryFixture(t, store)
	servePipe(t, store, cfo)
	delivered(t, store, t.TempDir(), "after-card.webp", webpPicture, false)
	response := httptest.NewRecorder()

	// Act
	NewHTTP(&Service{Store: store}, "board.local", nil).ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/reviews/board-card-text-fits/document", nil))

	// Assert
	if response.Code != 404 {
		t.Fatalf("the old file link = %d, want 404", response.Code)
	}
}
