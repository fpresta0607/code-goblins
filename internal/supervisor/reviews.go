package supervisor

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// maxReviews bounds the review list. An open item, a wait its goblin still
// stands on, or an item whose page is still watched is never dropped to make
// room: a new one waits in the inbox until an older one closes.
const maxReviews = 128

// maxReviewImages is the most images one review carries.
const maxReviewImages = 12

// closedReviewRetention is how long an answered, cleared or withdrawn item and
// its copied images stay listed before they are pruned.
const closedReviewRetention = 7 * 24 * time.Hour

// reviewID keeps a reporter-chosen ID safe in a URL path segment.
var reviewID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{7,127}$`)

// Review is an item for the Overlord that stays open until he answers or
// clears it or its reporter withdraws it; nothing expires an open item. Its
// images are copied under state/reviews when it is published, so they outlive
// the reporter's worktree and the goblin itself.
type Review struct {
	ID       string `json:"id"`
	Identity string `json:"identity"`
	// Made is the identity the item was made under, once it has followed the
	// CFO to another registration: its directory stays named after it.
	Made  string `json:"made,omitempty"`
	Task  string `json:"task,omitempty"`
	Title string `json:"title"`
	// ImageSums are the SHA-256 of each copied image in order. They make a
	// republish with the same ID and content change nothing and refuse other
	// content, and the board only ever sees ImageCount.
	ImageSums  []string `json:"image_sums,omitempty"`
	ImageCount int      `json:"image_count,omitempty"`
	Lavish     string   `json:"lavish,omitempty"`
	// LavishPage is the HTML file of a Lavish page, a goblin's or the CFO's
	// own, which the supervisor polls for the Overlord's feedback until the
	// review on it settles, whatever becomes of the item.
	LavishPage string `json:"lavish_page,omitempty"`
	// PageSettled is when the review on the item's page settled: the
	// Overlord answered or ended it there, an agent ended it, it could not be
	// polled, or its goblin's task was retired. The page is watched until
	// then, after the item closes too, so nothing he sends there goes unread.
	PageSettled *time.Time `json:"page_settled,omitempty"`
	// Link is the web link a goblin's wait gives as where the Overlord goes,
	// the only link its card opens.
	Link string `json:"link,omitempty"`
	// Follows is the decision a goblin's wait on him that opens nothing
	// follows, by its question's ID, which the goblin declared as it raised
	// the wait (cfo notify --follows): the CFO's question for its task (cfo
	// question --for) or the goblin's own with choices. The board shows only
	// that question while it is open, and its answer closes the wait. A wait
	// that declares none is its own card, whatever is open.
	Follows string `json:"follows,omitempty"`
	// Document is a delivered file, copied beside the Scrawl page that shows
	// it, which is the item's page.
	Document *ReviewDocument `json:"document,omitempty"`
	State    string          `json:"state"`
	Answer   string          `json:"answer,omitempty"`
	AnswerID string          `json:"answer_id,omitempty"`
	// Delivered says the answer reached the reporter itself; an answer for
	// a goblin that restarted or ended goes to the CFO and stays false.
	Delivered bool `json:"delivered,omitempty"`
	// AnsweredBy and AnsweredIn say who answered an item outside the Command
	// Center and where, such as overlord and page for his answer on the
	// item's own page, which the CFO has.
	AnsweredBy string `json:"answered_by,omitempty"`
	AnsweredIn string `json:"answered_in,omitempty"`
	// WindowClosedAt is when the review window of the item's page last
	// disconnected; the review goes on, and the board says the window closed.
	WindowClosedAt *time.Time `json:"window_closed_at,omitempty"`
	// RevisingSince is when the Overlord sent a revision from the item's page
	// without ending its review: the item then waits on its reporter's next
	// version, which replaces the page in this same item, not on him.
	RevisingSince *time.Time `json:"revising_since,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func validReview(r Review) error {
	if !reviewID.MatchString(r.ID) || len(r.Identity) != 64 || strings.TrimSpace(r.Title) == "" || len(r.Title) > 4000 || r.CreatedAt.IsZero() || r.CreatedAt.After(time.Now().Add(5*time.Minute)) {
		return errors.New("a review needs an ID of 8 to 128 letters, digits, dots, dashes or underscores, its reporter's identity, a timestamp and a title of at most 4000 characters")
	}
	if r.Task != "" && state.ValidTaskID(r.Task) != nil {
		return errors.New("invalid review task")
	}
	if len(r.ImageSums) > maxReviewImages || len(r.ImageSums) > 0 && r.Task == "" {
		return fmt.Errorf("only a goblin's review takes images, at most %d", maxReviewImages)
	}
	for _, sum := range r.ImageSums {
		if _, err := hex.DecodeString(sum); err != nil || len(sum) != 64 {
			return errors.New("invalid review image digest")
		}
	}
	if r.Lavish != "" {
		if problem := PresentationURLProblem(r.Lavish); problem != "" {
			return errors.New("a review's Scrawl link " + problem)
		}
	}
	if r.Link != "" {
		if problem := PresentationURLProblem(r.Link); problem != "" {
			return errors.New("a wait's link " + problem)
		}
	}
	if r.LavishPage != "" {
		extension := strings.ToLower(filepath.Ext(r.LavishPage))
		if r.Lavish == "" || !filepath.IsAbs(r.LavishPage) || filepath.Clean(r.LavishPage) != r.LavishPage || extension != ".html" && extension != ".htm" {
			return errors.New("a review's Scrawl page must be the absolute path of an HTML page, beside its link")
		}
	}
	if r.Follows != "" && (r.Task == "" || !strings.HasPrefix(r.ID, "waiting-"+r.Task+"-") || r.Lavish != "" || r.Link != "" || len(r.Follows) < 8 || len(r.Follows) > 128 || strings.ContainsAny(r.Follows, "\x00\r\n")) {
		return errors.New("only a goblin's wait that opens nothing follows a decision, named by its question's ID")
	}
	if r.Document != nil {
		if len(r.ImageSums) > 0 || r.Link != "" {
			return errors.New("a delivered file carries no images or link beside its Scrawl page")
		}
		return validDocument(*r.Document)
	}
	return nil
}

// sameReview reports whether b publishes what a does. A delivered file's page
// is made for it each time it is delivered, so its file names it.
func sameReview(a, b Review) bool {
	if a.Document != nil || b.Document != nil {
		return a.Identity == b.Identity && a.Task == b.Task && a.Title == b.Title && a.Document != nil && b.Document != nil && *a.Document == *b.Document
	}
	return a.Identity == b.Identity && a.Task == b.Task && a.Title == b.Title && a.Lavish == b.Lavish && a.LavishPage == b.LavishPage && a.Link == b.Link && a.Follows == b.Follows && slices.Equal(a.ImageSums, b.ImageSums)
}

// reviewImageDir holds one publication's copied images, named by position, or
// its document. Its name is that publication's own digest, so no other
// record, not even a republish of the same ID, ever names it.
func reviewImageDir(stateDir string, r Review) string {
	parts := []string{r.ID, madeUnder(r.Made, r.Identity), strconv.FormatInt(r.CreatedAt.UnixNano(), 10), strings.Join(r.ImageSums, ",")}
	if r.Document != nil {
		parts = append(parts, r.Document.Sum)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return filepath.Join(stateDir, "reviews", hex.EncodeToString(sum[:]))
}

// reviewReporter proves who is reporting: a goblin from inside its task's own
// terminal, or the registered primary CFO when taskID is empty. The returned
// release keeps the CFO's registration from changing until the report is made.
func reviewReporter(h home.Home, taskID string) (string, func(), error) {
	if taskID != "" {
		meta, err := goblinAsker(h.State, taskID)
		if err != nil {
			return "", nil, err
		}
		return goblinIdentity(meta), func() {}, nil
	}
	return (&CFOConnection{State: h.State}).CallerIdentity()
}

// PublishReview reports an item for the Overlord from the reporter's own
// process. Images are checked the way a question's are and copied under
// state/reviews before the item is recorded. A page, given only with its
// link, is polled by the supervisor for his feedback; a Scrawl link given
// without its page is polled on the page lavish-axi serves there. While AFK
// mode is on a goblin's item with a page is that goblin's question to the CFO
// instead, which answers it in his place.
func PublishReview(h home.Home, taskID, id, title, lavish, page string, images []string) error {
	if len(images) > maxReviewImages || len(images) > 0 && taskID == "" {
		return fmt.Errorf("only a goblin's review takes images, at most %d", maxReviewImages)
	}
	if lavish != "" && page == "" {
		var err error
		if page, err = servedPage(lavish); err != nil {
			return err
		}
	}
	if switched, err := afk.Read(h.State); taskID != "" && lavish != "" && err == nil && switched.On {
		return askCFOOnPage(h, taskID, id, title, lavish, page, images)
	}
	return publishItem(h, Review{ID: id, Task: taskID, Title: title, Lavish: lavish, LavishPage: page}, func(r *Review, dir string) (bool, error) {
		sums, err := stageReviewImages(h, taskID, images, dir)
		r.ImageSums = sums
		return len(images) > 0, err
	}, nil)
}

// servedPage is the HTML page of the review session lavish-axi keeps at link,
// whose address ends with /session/<key> on whichever of its addresses link
// names, or "" for a link to no such session.
func servedPage(link string) (string, error) {
	u, err := url.Parse(link)
	if err != nil {
		return "", nil
	}
	key, found := strings.CutPrefix(u.Path, "/session/")
	if !found || key == "" || strings.Contains(key, "/") {
		return "", nil
	}
	sessions, err := (axi.Lavish{}).Sessions()
	if err != nil {
		return "", err
	}
	for _, session := range sessions {
		extension := strings.ToLower(filepath.Ext(session.File))
		if session.Key == key && filepath.IsAbs(session.File) && filepath.Clean(session.File) == session.File && (extension == ".html" || extension == ".htm") {
			return session.File, nil
		}
	}
	return "", nil
}

// askCFOOnPage puts goblin taskID's item id, with its page, to the CFO as the
// goblin's question while AFK mode is on, once for the item: a notify that
// wakes the CFO with the item's title, the page's link and the choices the
// page declares, and the question on the board, which is the CFO's to answer
// and never waits on the Overlord. The item's images, which his card would
// show, are named for the CFO, which answers in his place; the goblin writes
// no status line, as for any item.
func askCFOOnPage(h home.Home, taskID, id, title, link, page string, images []string) error {
	meta, err := goblinAsker(h.State, taskID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := validReview(Review{ID: id, Identity: goblinIdentity(meta), Task: taskID, Title: title, Lavish: link, LavishPage: page, CreatedAt: now}); err != nil {
		return err
	}
	asked := strings.TrimSpace(title)
	if len(images) > 0 {
		named := make([]string, len(images))
		for i, image := range images {
			if named[i], err = filepath.Abs(image); err != nil {
				return err
			}
		}
		asked += " (images " + strings.Join(named, ", ") + ")"
	}
	detail := "blocked: " + asked + " (page " + link + ")" + PageOptions(page)
	record, isNew, err := wake.AppendFirst(h.State, "review/"+taskID+"/"+id, "notify", taskID, state.NormalizeStatusDetail(detail))
	if err != nil || !isNew {
		return err
	}
	if _, err := wake.PublishEpisode(h.State); err != nil {
		return err
	}
	return SurfaceNotify(h.State, taskID, record, detail, link, nil)
}

// ErrPageHeld refuses an item whose page another reporter's open item
// already shows: his answer on a page reaches the one reporter whose item
// stands for it, so a second reporter's item would take it from the first.
var ErrPageHeld = errors.New("another reporter's open item already shows that page")

// refuseHeldPage refuses item r while another reporter's open item, the CFO's
// or another goblin's, shows r's page. A reporter's own newer item for its
// own page is its own. The caller holds the publish lock, so no other report
// records the page between this check and r's record.
func refuseHeldPage(stateDir string, r Review) error {
	if r.LavishPage == "" {
		return nil
	}
	reviews, err := reportedReviews(stateDir)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(reviews, func(held Review) bool {
		return held.State == "open" && held.Task != r.Task && held.LavishPage != "" && pageKey(held.LavishPage) == pageKey(r.LavishPage)
	})
	if i < 0 {
		return nil
	}
	reporter := "the CFO"
	if reviews[i].Task != "" {
		reporter = reviews[i].Task
	}
	return fmt.Errorf("%w: %s's item %s, and his answer on that page reaches %s, so there is nothing to publish", ErrPageHeld, reporter, reviews[i].ID, reporter)
}

// publishItem records r for its reporter, proven the way reviewReporter does.
// stage fills in what r copies into dir and says whether it copied anything;
// the copies move beside the item's record only once the item is new. placed,
// when set, runs once they are in their own folder, before the item is
// recorded, such as to open the page they hold. A page another reporter's
// open item shows is refused before anything is staged.
func publishItem(h home.Home, r Review, stage func(r *Review, dir string) (bool, error), placed func(r *Review, dir string) error) error {
	identity, release, err := reviewReporter(h, r.Task)
	if err != nil {
		return err
	}
	defer release()
	now := time.Now().UTC()
	r.Identity, r.State, r.CreatedAt, r.UpdatedAt = identity, "open", now, now
	if err := validReview(r); err != nil {
		return err
	}
	unlock, err := reviewPublishLock(h.State)
	if err != nil {
		return err
	}
	defer unlock()
	if err := reviewInboxRoom(h.State); err != nil {
		return err
	}
	if err := refuseHeldPage(h.State, r); err != nil {
		return err
	}
	staged := filepath.Join(h.State, "reviews", ".staged")
	if err := fsx.RemoveAll(staged); err != nil {
		return err
	}
	defer fsx.RemoveAll(staged)
	copied, err := stage(&r, staged)
	if err != nil {
		return err
	}
	if err := validReview(r); err != nil {
		return err
	}
	prior, found, err := reportedReview(h.State, r.ID)
	if err != nil {
		return err
	}
	if found {
		if !sameReview(prior, r) {
			return errors.New("review ID already used")
		}
		return nil
	}
	if !copied {
		return recordReview(h.State, r)
	}
	final := reviewImageDir(h.State, r)
	if err := os.Rename(staged, final); err != nil {
		return err
	}
	if placed != nil {
		if err := placed(&r, final); err != nil {
			return errors.Join(err, fsx.RemoveAll(final))
		}
		if err := validReview(r); err != nil {
			return errors.Join(err, fsx.RemoveAll(final))
		}
	}
	if err := recordReview(h.State, r); err != nil {
		return errors.Join(err, fsx.RemoveAll(final))
	}
	return nil
}

// WithdrawReview closes the reporter's own open item with its reason.
func WithdrawReview(h home.Home, taskID, id, reason string) error {
	identity, release, err := reviewReporter(h, taskID)
	if err != nil {
		return err
	}
	defer release()
	if !reviewID.MatchString(id) || strings.TrimSpace(reason) == "" || len(reason) > 2000 {
		return errors.New("a withdrawal names the review ID and a reason of at most 2000 characters")
	}
	unlock, err := reviewPublishLock(h.State)
	if err != nil {
		return err
	}
	defer unlock()
	prior, found, err := reportedReview(h.State, id)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("no review with that ID to withdraw")
	}
	if prior.Identity != identity {
		return errors.New("only the reporter that published a review can withdraw it")
	}
	if prior.State != "open" {
		return errors.New("the review is already " + prior.State)
	}
	return recordReview(h.State, Review{ID: id, Identity: identity, Task: taskID, State: "withdrawn", Reason: reason, UpdatedAt: time.Now().UTC()})
}

// ClearReview closes any open item for the registered primary CFO, such as a
// retired goblin's item that only its reporter could withdraw. The reason is
// kept on the item and state/reviews.audit records the clear.
func ClearReview(h home.Home, id, reason string) error {
	identity, release, err := reviewReporter(h, "")
	if err != nil {
		return err
	}
	defer release()
	if !reviewID.MatchString(id) || strings.TrimSpace(reason) == "" || len(reason) > 1900 {
		return errors.New("a clear names the review ID and a reason of at most 1900 characters")
	}
	unlock, err := reviewPublishLock(h.State)
	if err != nil {
		return err
	}
	defer unlock()
	reviews, err := reportedReviews(h.State)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(reviews, func(r Review) bool { return r.ID == id })
	if i < 0 {
		return errors.New("no review with that ID to clear")
	}
	if r := reviews[i]; r.State != "open" {
		return errors.New("the review is already " + r.State)
	}
	return recordReview(h.State, Review{ID: id, Identity: identity, Task: reviews[i].Task, State: "cleared", Reason: "Cleared by the CFO: " + strings.TrimSpace(reason), UpdatedAt: time.Now().UTC()})
}

// reviewPublishLock serializes reporters, waiting briefly for another one.
func reviewPublishLock(stateDir string) (func(), error) {
	_, err := lock.AcquireExclusiveNamed(stateDir, ".review-publish.lock")
	for deadline := time.Now().Add(2 * time.Second); err != nil && time.Now().Before(deadline); {
		time.Sleep(25 * time.Millisecond)
		_, err = lock.AcquireExclusiveNamed(stateDir, ".review-publish.lock")
	}
	if err != nil {
		return nil, err
	}
	return func() { _ = lock.ReleaseExclusiveNamed(stateDir, ".review-publish.lock") }, nil
}

// stageReviewImages checks each image like a question's and copies it into
// dir by position, returning each copy's SHA-256.
func stageReviewImages(h home.Home, taskID string, images []string, dir string) ([]string, error) {
	if len(images) == 0 {
		return nil, nil
	}
	meta, err := state.ReadTaskMeta(h.State, taskID)
	if err != nil {
		return nil, fmt.Errorf("task %s has no live record: %w", taskID, err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	sums := make([]string, 0, len(images))
	for n, image := range images {
		source, _, err := openReviewImage(reviewImageRoots(h, meta), image)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", image, err)
		}
		sum, err := copyReviewImage(source, filepath.Join(dir, strconv.Itoa(n)))
		source.Close()
		if err != nil {
			return nil, err
		}
		sums = append(sums, sum)
	}
	return sums, nil
}

func copyReviewImage(source io.Reader, path string) (string, error) {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(source, maxReviewImage))
	closeErr := out.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// reportedReviews lists every item reported so far, the way reportedReview
// finds one: an open publication still in the inbox and the supervisor's
// record.
func reportedReviews(stateDir string) ([]Review, error) {
	var reviews []Review
	entries, err := os.ReadDir(filepath.Join(stateDir, "reviews-inbox"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".open.json") {
			continue
		}
		data, err := fsx.ReadFile(filepath.Join(stateDir, "reviews-inbox", entry.Name()))
		if err != nil {
			return nil, err
		}
		var r Review
		if json.Unmarshal(data, &r) == nil {
			reviews = append(reviews, r)
		}
	}
	if info, err := os.Stat(filepath.Join(stateDir, ".supervisor.json")); err == nil {
		if info.Size() > maxStateBytes {
			return nil, errors.New("supervisor state exceeds its bound")
		}
		data, err := fsx.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
		if err != nil {
			return nil, err
		}
		var db Database
		if err := json.Unmarshal(data, &db); err != nil {
			return nil, errors.New("supervisor review history is unreadable")
		}
		for _, r := range db.Reviews {
			if !slices.ContainsFunc(reviews, func(prior Review) bool { return prior.ID == r.ID }) {
				reviews = append(reviews, r)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return reviews, nil
}

// reportedReview finds a review waiting in the inbox or already recorded. It
// only reads: opening a Store here would run crash recovery under a live
// supervisor. The inbox is read first because ingest records an item before it
// removes the inbox copy, so one of the two reads always sees it.
func reportedReview(stateDir, id string) (Review, bool, error) {
	data, err := fsx.ReadFile(reviewInboxPath(stateDir, id, "open"))
	if err == nil {
		var prior Review
		if err := json.Unmarshal(data, &prior); err != nil {
			return Review{}, false, errors.New("review ID already used")
		}
		return prior, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Review{}, false, err
	}
	if info, err := os.Stat(filepath.Join(stateDir, ".supervisor.json")); err == nil {
		if info.Size() > maxStateBytes {
			return Review{}, false, errors.New("supervisor state exceeds its bound")
		}
		data, err := fsx.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
		if err != nil {
			return Review{}, false, err
		}
		var db Database
		if err := json.Unmarshal(data, &db); err != nil {
			return Review{}, false, errors.New("supervisor review history is unreadable")
		}
		if i := slices.IndexFunc(db.Reviews, func(r Review) bool { return r.ID == id }); i >= 0 {
			return db.Reviews[i], true, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Review{}, false, err
	}
	return Review{}, false, nil
}

// A publication and a withdrawal of one item wait in the inbox side by side,
// so a withdrawal can never overwrite a publication not yet ingested.
func reviewInboxPath(stateDir, id, reviewState string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(stateDir, "reviews-inbox", hex.EncodeToString(sum[:])+"."+reviewState+".json")
}

// reviewInboxRoom refuses a report while the inbox is full. Reporters hold the
// publish lock and ingest only removes records, so the answer holds until the
// report is spooled.
func reviewInboxRoom(stateDir string) error {
	dir := filepath.Join(stateDir, "reviews-inbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) >= 2*maxReviews {
		return errors.New("the review inbox is full")
	}
	return nil
}

// cfoRecord reports whether a review record speaks for the registered CFO:
// its own item, its own withdrawal, or a clear, which only the CFO gives.
func cfoRecord(r Review) bool {
	return r.Task == "" || r.State == "cleared"
}

// recordReview hands a review record to the supervisor: the CFO's over the
// pipe, where the supervisor proves who is sending it, and a goblin's through
// the inbox, where it can only ever read as that goblin's.
func recordReview(stateDir string, r Review) error {
	if cfoRecord(r) {
		return sendPipeRequest(stateDir, runPipeRequest{Kind: "review", Review: &r})
	}
	return spoolReview(stateDir, r)
}

func spoolReview(stateDir string, r Review) error {
	if err := reviewInboxRoom(stateDir); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(reviewInboxPath(stateDir, r.ID, r.State), data)
}

func (s *Store) ingestReviews() error {
	dir := filepath.Join(s.Home.State, "reviews-inbox")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type record struct {
		path    string
		review  Review
		invalid error
	}
	var records []record
	for _, entry := range entries[:min(len(entries), 2*maxReviews)] {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var r Review
		var invalid error
		if info.Size() > 16<<10 {
			invalid = errors.New("review exceeds its size limit")
		} else {
			data, err := fsx.ReadFile(path)
			if err != nil {
				return err
			}
			if json.Unmarshal(data, &r) != nil {
				invalid = errors.New("invalid review JSON")
			}
		}
		if invalid == nil && cfoRecord(r) {
			invalid = errFromInbox
		}
		records = append(records, record{path, r, invalid})
	}
	// Publications first, oldest first, so a withdrawal finds its item.
	slices.SortStableFunc(records, func(a, b record) int {
		if (a.review.State == "open") != (b.review.State == "open") {
			if a.review.State == "open" {
				return -1
			}
			return 1
		}
		return a.review.UpdatedAt.Compare(b.review.UpdatedAt)
	})
	for _, record := range records {
		invalid := record.invalid
		if invalid == nil {
			s.mu.Lock()
			if slices.ContainsFunc(s.db.Reviews, func(prior Review) bool { return prior.ID == record.review.ID && prior.Task == "" }) {
				invalid = errFromInbox
			}
			s.mu.Unlock()
		}
		if invalid == nil {
			invalid = s.acceptReview(record.review)
		}
		if errors.Is(invalid, ErrStorage) {
			return invalid
		}
		if errors.Is(invalid, ErrDeferred) {
			continue
		}
		if invalid != nil {
			s.mu.Lock()
			s.db.Issues = append(s.db.Issues, "Review report rejected: "+bounded(invalid.Error(), 300))
			if len(s.db.Issues) > 20 {
				s.db.Issues = s.db.Issues[len(s.db.Issues)-20:]
			}
			err := s.save()
			s.mu.Unlock()
			if err != nil {
				return err
			}
		}
		if err := os.Remove(record.path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) acceptReview(r Review) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Reviews, func(prior Review) bool { return prior.ID == r.ID })
	switch r.State {
	case "open":
		r.Made = ""
		if err := validReview(r); err != nil {
			return err
		}
		if i >= 0 {
			// A publication that raced one already recorded leaves nothing.
			if dir := reviewImageDir(s.Home.State, r); dir != reviewImageDir(s.Home.State, s.db.Reviews[i]) {
				if err := fsx.RemoveAll(dir); err != nil {
					return err
				}
			}
			if !sameReview(s.db.Reviews[i], r) {
				return errors.New("review ID already used")
			}
			return nil
		}
		if len(s.db.Reviews) >= maxReviews {
			closed := slices.IndexFunc(s.db.Reviews, func(old Review) bool {
				return old.State != "open" && !s.answering(old) && !s.holdsWait(old) && !s.watchesPage(old)
			})
			if closed < 0 {
				return ErrDeferred
			}
			if err := fsx.RemoveAll(reviewImageDir(s.Home.State, s.db.Reviews[closed])); err != nil {
				return err
			}
			s.db.Reviews = slices.Delete(s.db.Reviews, closed, closed+1)
		}
		r.ImageCount, r.Reason, r.Answer, r.AnswerID, r.Delivered, r.PageSettled = 0, "", "", "", false, nil
		s.db.Reviews = append(s.db.Reviews, r)
		// A wait that follows a decision already answered was answered with it.
		if q := slices.IndexFunc(s.db.Questions, func(q Question) bool { return q.ID == r.Follows }); r.Follows != "" && q >= 0 && isAnswered(s.db.Questions[q]) {
			s.closeFollowUps(s.db.Questions[q], cmp.Or(s.db.Questions[q].AnsweredBy, "overlord"), time.Now().UTC())
		}
	case "withdrawn":
		if i < 0 {
			// Its publication still waits in the inbox while 128 are open.
			if _, err := os.Stat(reviewInboxPath(s.Home.State, r.ID, "open")); err == nil {
				return ErrDeferred
			}
			return errors.New("no review with that ID to withdraw")
		}
		prior := &s.db.Reviews[i]
		if prior.Identity != r.Identity || prior.Task != r.Task {
			return errors.New("only the reporter that published a review can withdraw it")
		}
		if prior.State != "open" {
			return nil
		}
		prior.State, prior.Reason, prior.UpdatedAt = "withdrawn", bounded(r.Reason, 2000), r.UpdatedAt
	case "cleared":
		if i < 0 {
			if _, err := os.Stat(reviewInboxPath(s.Home.State, r.ID, "open")); err == nil {
				return ErrDeferred
			}
			return errors.New("no review with that ID to clear")
		}
		if identity, err := s.primaryIdentity(); err != nil || identity != r.Identity {
			return errors.New("only the registered CFO can clear another reporter's review")
		}
		prior := &s.db.Reviews[i]
		if prior.State != "open" {
			return nil
		}
		prior.State, prior.Reason, prior.UpdatedAt = "cleared", bounded(r.Reason, 2000), r.UpdatedAt
		s.auditClear(*prior)
	case "version":
		// A wait's next version of its page replaces the item in place: the
		// same item, with its new words and links, waiting on the Overlord
		// again, so the Command Center never opens a second one for it.
		if i < 0 {
			if _, err := os.Stat(reviewInboxPath(s.Home.State, r.ID, "open")); err == nil {
				return ErrDeferred
			}
			return errors.New("no review with that ID for its next version")
		}
		prior := &s.db.Reviews[i]
		if prior.Identity != r.Identity || prior.Task != r.Task || prior.LavishPage == "" || prior.LavishPage != r.LavishPage {
			return errors.New("only a page's reporter can replace it with the next version of that page")
		}
		if s.answering(*prior) {
			return ErrDeferred
		}
		next := *prior
		next.Title, next.Lavish, next.Link = r.Title, r.Lavish, r.Link
		next.State, next.Reason, next.Answer, next.AnswerID, next.Delivered, next.AnsweredBy, next.AnsweredIn = "open", "", "", "", false, "", ""
		next.RevisingSince, next.WindowClosedAt, next.PageSettled, next.CreatedAt, next.UpdatedAt = nil, nil, nil, r.UpdatedAt, r.UpdatedAt
		if err := validReview(next); err != nil {
			return err
		}
		*prior = next
	default:
		return errors.New("a review report is open, a next version, withdrawn or cleared")
	}
	return s.save()
}

// primaryIdentity is the identity of the CFO primary.json registers now.
func (s *Store) primaryIdentity() (string, error) {
	file, err := openPrimary(filepath.Join(s.Home.State, "primary.json"))
	if err != nil {
		return "", errNotRegistered
	}
	defer file.Close()
	_, identity, err := decodePrimary(file)
	return identity, err
}

// auditClear records the clear of item r in state/reviews.audit, or says
// among the board's issues that it could not. The caller holds the store lock
// and saves.
func (s *Store) auditClear(r Review) {
	if err := appendReviewAudit(s.Home.State, r); err != nil {
		s.db.Issues = append(s.db.Issues, r.ID+" was cleared, but state/reviews.audit was not written: "+bounded(err.Error(), 300))
		if len(s.db.Issues) > 20 {
			s.db.Issues = s.db.Issues[len(s.db.Issues)-20:]
		}
	}
}

// appendReviewAudit records a clear of an item in state/reviews.audit:
// when, the item, its goblin and the reason, on one line.
func appendReviewAudit(stateDir string, r Review) error {
	task := r.Task
	if task == "" {
		task = "-"
	}
	f, err := fsx.OpenAppend(filepath.Join(stateDir, "reviews.audit"), 0600)
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintf(f, "%s %s %s %s\n", r.UpdatedAt.UTC().Format(time.RFC3339), r.ID, task, strings.Join(strings.Fields(r.Reason), " "))
	return errors.Join(werr, f.Close())
}

// answering reports whether an item's answer is still queued or running; the
// item keeps its place until the answer is delivered or refused.
func (s *Store) answering(r Review) bool {
	return r.AnswerID != "" && slices.ContainsFunc(s.db.Actions, func(a Action) bool {
		return a.ID == r.AnswerID && (a.Status == "queued" || a.Status == "running")
	})
}

// holdsWait reports whether a closed item is the wait on the Overlord its
// goblin still stands on. The board reads the goblin past that wait only while
// the item is listed, so it stays until the goblin reports anything newer
// than a question, or its task is gone. Only a wait item reads its task's files; one whose task
// cannot be read is kept for this pass, the safe side, since the hold is one
// item per task and the next pass checks it again.
func (s *Store) holdsWait(r Review) bool {
	if r.State == "open" || r.Task == "" || !strings.HasPrefix(r.ID, "waiting-"+r.Task+"-") {
		return false
	}
	if _, err := state.ReadTaskMeta(s.Home.State, r.Task); errors.Is(err, fs.ErrNotExist) {
		return false
	} else if err != nil {
		return true
	}
	lines, err := state.TailStatus(s.Home.State, r.Task, 50)
	if err != nil {
		return true
	}
	reportedAt, report := standingReport(lines, time.Time{})
	return waitStands(r, reportedAt, report)
}

// waitStands reports whether item r is a goblin's wait on the Overlord and the
// goblin's standing report is still that wait, not a newer one.
// endsAWait reports whether a goblin's report after its wait on the Overlord
// ends that wait: it finished or failed, or waits on him again, which a newer
// item stands for. A goblin working on beside the wait, waiting on something
// else meanwhile or asking the CFO still wants his answer, as a goblin that
// never waits on a choice it can undo does: pd-whats-new's picks were
// withdrawn by its own working report on 2026-10-08 and asked again fourteen
// hours later.
func endsAWait(report string) bool {
	return strings.HasPrefix(report, "done: ") || strings.HasPrefix(report, "failed: ") || strings.HasPrefix(report, "waiting on overlord: ")
}

func waitStands(r Review, reportedAt time.Time, report string) bool {
	return strings.HasPrefix(r.ID, "waiting-"+r.Task+"-") && strings.HasPrefix(report, "waiting on overlord: ") && !reportedAt.After(r.CreatedAt)
}

// pruneReviews drops closed items, with their copied images, a set time after
// they closed. Open items, items whose answer is on its way, a wait its
// goblin still stands on and an item whose page is still watched are never
// pruned.
func (s *Store) pruneReviews(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]Review, 0, len(s.db.Reviews))
	var removed []Review
	for _, r := range s.db.Reviews {
		if r.State != "open" && !s.answering(r) && now.Sub(r.UpdatedAt) > closedReviewRetention && !s.holdsWait(r) && !s.watchesPage(r) {
			removed = append(removed, r)
			continue
		}
		kept = append(kept, r)
	}
	if len(removed) == 0 {
		return nil
	}
	s.db.Reviews = kept
	if err := s.save(); err != nil {
		return err
	}
	// Removed only after the list no longer names them; a failure leaves
	// unreferenced files, never a listed item without its images.
	var err error
	for _, r := range removed {
		err = errors.Join(err, fsx.RemoveAll(reviewImageDir(s.Home.State, r)))
	}
	return err
}

// clearedFromTheCommandCenter is the reason an item the Overlord cleared on
// the board reads, in state/reviews.audit as on the item.
const clearedFromTheCommandCenter = "Cleared from the Command Center."

// clearReview closes open item i, which the Overlord cleared at at, with
// reason, and records the clear in state/reviews.audit. It closes only that
// item: a question its goblin asked stays its own. The caller holds the store
// lock.
func (s *Store) clearReview(i int, reason string, at time.Time) {
	s.db.Reviews[i].State, s.db.Reviews[i].Reason, s.db.Reviews[i].UpdatedAt = "cleared", reason, at
	s.auditClear(s.db.Reviews[i])
}

// answerReview delivers the Overlord's answer once to the item's reporter:
// the goblin's own pane while it is the same task generation, or the CFO that
// reported it, where a reporter inside a long turn takes it once the turn
// ends. An answer for a goblin that restarted or ended goes to the
// current CFO instead, and the item stays undelivered. An answer a goblin
// received also reaches the CFO, as a notice in its wake queue.
func (s *Service) answerReview(ctx context.Context, a Action) (Evaluation, error) {
	if s.Options.CFO == nil {
		return Evaluation{}, fmt.Errorf("%w: message transport is unavailable", ErrRejected)
	}
	reviews := s.Store.Snapshot().Reviews
	i := slices.IndexFunc(reviews, func(r Review) bool { return r.ID == a.ReviewID && r.Identity == a.Generation && r.AnswerID == a.ID })
	if i < 0 {
		return Evaluation{}, fmt.Errorf("%w: the review changed; nothing was sent", ErrRejected)
	}
	r := reviews[i]
	var result Evaluation
	var err error
	sent := time.Now().UTC()
	if r.Task == "" {
		result, err = s.Options.CFO.Send(ctx, r.Identity, fmt.Sprintf("Answer to your review item %s (%s): %s", r.ID, r.Title, a.Text))
	} else {
		text := fmt.Sprintf("The Overlord answered your review item %s (%s): %s", r.ID, r.Title, a.Text)
		result, err = s.Options.CFO.SendGoblin(ctx, r.Task, r.Identity, text)
		if errors.Is(err, ErrRejected) {
			return s.answerReviewToCFO(ctx, r, a.Text)
		}
		if errors.Is(err, fleet.ErrQueuedForToolCall) {
			result, err = s.behindGoblinsTurn(r.Task, sent, text, "Submitted while its reporter was working; it takes the answer at its next tool call, or as its current turn ends."), nil
		}
	}
	if err != nil {
		return result, err
	}
	if r.Task != "" {
		// The goblin already has the answer, so a notice that cannot be
		// queued is reported rather than failing, and resending, the answer.
		if err := noticeAnswerToCFO(s.Store.Home.State, r, a.Text); err != nil {
			s.publish(err)
		}
	}
	if result.Awaiting != nil {
		// Sent, and delivered once its reporter's hook says it took it.
		return result, nil
	}
	return result, s.Store.markReviewDelivered(r.ID, a.ID)
}

// noticeAnswerToCFO tells the CFO what the Overlord answered on a goblin's
// item, without asking it anything.
func noticeAnswerToCFO(stateDir string, r Review, answer string) error {
	if _, err := wake.Append(stateDir, "review", r.Task, bounded(fmt.Sprintf("the Overlord answered %s (%s) on the board, and the goblin has it: %s", r.ID, r.Title, answer), 4000)); err != nil {
		return err
	}
	_, err := wake.PublishEpisode(stateDir)
	return err
}

// tellOfAClearedWait tells a goblin whose wait on the Overlord he cleared from
// the Command Center that it is gone from there, since the goblin still waits
// on him, and tells the CFO, so a wait he closed is never lost unseen. It
// returns what could not be told, for the clear's own reason.
func (s *Service) tellOfAClearedWait(ctx context.Context, id string) string {
	reviews := s.Store.Snapshot().Reviews
	i := slices.IndexFunc(reviews, func(r Review) bool { return r.ID == id })
	if i < 0 || reviews[i].Task == "" || !strings.HasPrefix(id, "waiting-"+reviews[i].Task+"-") {
		return ""
	}
	r := reviews[i]
	told := "and the goblin was told"
	if s.Options.CFO == nil {
		told = "but the goblin could not be told: message transport is unavailable"
	} else if _, err := s.Options.CFO.SendGoblin(ctx, r.Task, r.Identity, fmt.Sprintf("The Overlord skipped your wait %s (%s) from the Command Center. Go on without it. If you still need him for it, ask again with cfo notify --waiting-on overlord.", r.ID, r.Title)); err != nil && !errors.Is(err, fleet.ErrQueuedForToolCall) {
		told = "but the goblin could not be told: " + bounded(err.Error(), 300)
	}
	detail := fmt.Sprintf("the Overlord cleared %s's wait %s (%s) from the Command Center, %s", r.Task, r.ID, r.Title, told)
	if _, err := wake.Append(s.Store.Home.State, "review", r.Task, bounded(detail, 4000)); err != nil {
		return " The CFO could not be told: " + bounded(err.Error(), 300)
	}
	if _, err := wake.PublishEpisode(s.Store.Home.State); err != nil {
		return " The CFO could not be told: " + bounded(err.Error(), 300)
	}
	return ""
}

// answerReviewToCFO hands an answer whose goblin is gone to the current CFO.
func (s *Service) answerReviewToCFO(ctx context.Context, r Review, answer string) (Evaluation, error) {
	file, err := openPrimary(filepath.Join(s.Store.Home.State, "primary.json"))
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: %s restarted or ended and no CFO is registered; the answer is kept on the item", ErrRejected, r.Task)
	}
	_, identity, err := decodePrimary(file)
	_ = file.Close()
	if err != nil {
		return Evaluation{}, fmt.Errorf("%w: %s restarted or ended and the CFO registration is unreadable; the answer is kept on the item", ErrRejected, r.Task)
	}
	result, err := s.Options.CFO.Send(ctx, identity, fmt.Sprintf("Answer to %s's review item %s (%s), which came to you because %s restarted or ended: %s", r.Task, r.ID, r.Title, r.Task, answer))
	if err != nil {
		return result, err
	}
	if result.Awaiting != nil {
		return Evaluation{Reason: r.Task + " had restarted or ended, so the answer went to the CFO, which takes it at its next tool call, or as its current turn ends.", Awaiting: result.Awaiting}, nil
	}
	return Evaluation{Reason: r.Task + " had restarted or ended, so the CFO received the answer."}, nil
}

// markReviewDelivered records that an answer reached the item's reporter. An
// item cannot close or be pruned while its answer is on its way, so it is
// always found.
func (s *Store) markReviewDelivered(id, answerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Reviews, func(r Review) bool { return r.ID == id && r.AnswerID == answerID })
	if i < 0 {
		return fmt.Errorf("the answered review %s is gone", id)
	}
	s.db.Reviews[i].Delivered = true
	return s.save()
}

// reviewImage serves image n of a review at /api/reviews/<id>/images/<n>,
// from the copy made when it was published and checked again on every
// request, and a review's page at /api/reviews/<id>/preview.
func (h *HTTP) reviewImage(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/reviews/"), "/")
	if len(parts) == 2 && parts[1] == "preview" {
		h.reviewPreview(w, r, parts[0])
		return
	}
	n, err := -1, error(nil)
	if len(parts) == 3 && parts[1] == "images" {
		n, err = strconv.Atoi(parts[2])
	}
	reviews := h.Service.Store.Snapshot().Reviews
	i := slices.IndexFunc(reviews, func(x Review) bool { return len(parts) == 3 && x.ID == parts[0] })
	if err != nil || i < 0 || n < 0 || n >= len(reviews[i].ImageSums) {
		apiError(w, 404, "Unknown review image")
		return
	}
	dir := reviewImageDir(h.Service.Store.Home.State, reviews[i])
	f, kind, err := openReviewImage([]string{dir}, filepath.Join(dir, strconv.Itoa(n)))
	if err != nil {
		apiError(w, 403, "The review image is no longer available")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Disposition", "inline")
	_, _ = io.Copy(w, io.LimitReader(f, maxReviewImage))
}

// PublishWait puts a goblin's wait on the Overlord in the Command Center as an
// item for him until he answers or clears it, the goblin withdraws it or its
// task is gone. It names the item waiting-<task>-<wake sequence>, which
// holdsWait relies on.
// why keeps the goblin's lines, so its card can show a table of values, and
// link is the web link it gives as where he goes, if any, and follows the ID
// of the open decision the goblin declared the wait to be, if any. A wait on
// a page the goblin already has open in an item is the page's next version,
// which replaces that item rather than opening another.
func PublishWait(h home.Home, taskID string, seq int, why, lavish, page, link, follows string) error {
	wait := Review{ID: fmt.Sprintf("waiting-%s-%d", taskID, seq), Task: taskID, Title: "Waiting on you: " + why, Lavish: lavish, LavishPage: page, Link: link, Follows: follows}
	if page != "" {
		reviews, err := reportedReviews(h.State)
		if err != nil {
			return err
		}
		if i := slices.IndexFunc(reviews, func(r Review) bool {
			return r.State == "open" && r.Task == taskID && r.LavishPage == page && strings.HasPrefix(r.ID, "waiting-"+taskID+"-")
		}); i >= 0 {
			wait.ID = reviews[i].ID
			return publishVersion(h, wait)
		}
	}
	return publishItem(h, wait, func(*Review, string) (bool, error) { return false, nil }, nil)
}

// publishVersion records the next version of its reporter's open item r,
// proven the way publishItem proves a new one.
func publishVersion(h home.Home, r Review) error {
	identity, release, err := reviewReporter(h, r.Task)
	if err != nil {
		return err
	}
	defer release()
	now := time.Now().UTC()
	r.Identity, r.State, r.CreatedAt, r.UpdatedAt = identity, "version", now, now
	if err := validReview(r); err != nil {
		return err
	}
	unlock, err := reviewPublishLock(h.State)
	if err != nil {
		return err
	}
	defer unlock()
	if err := refuseHeldPage(h.State, r); err != nil {
		return err
	}
	return recordReview(h.State, r)
}

// retireItems withdraws every item of a goblin whose task is gone, such as
// one cleaned up: a task is gone once its task record is. Nothing the goblin
// reports closes its item, done included, since he may not have seen it yet:
// only his answer, his clear or the goblin's own withdraw does. On 2026-10-15
// two goblins' done reports withdrew their unanswered cards before he saw
// either. A delivered file is never retired: its copy outlives the goblin,
// and it leaves the queue only once the Overlord answers on its page or
// clears it.
func (s *Store) retireItems() error {
	for _, r := range s.Snapshot().Reviews {
		if r.State != "open" || r.Task == "" || r.Document != nil {
			continue
		}
		_, err := state.ReadTaskMeta(s.Home.State, r.Task)
		if err == nil {
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := s.withdrawReview(r.ID, bounded(r.Task+" is gone", 2000)); err != nil {
			return err
		}
	}
	return nil
}

// closePageQuestionAsks closes the open items of the goblin that asked q
// showing the page AFK mode asked of the CFO as q, published before q: an
// answer to that question is the answer to that page, and to no other page,
// no later version of it and no wait. The caller holds the store lock.
func (s *Store) closePageQuestionAsks(q Question, by, reason string) {
	for i := range s.db.Reviews {
		if r := &s.db.Reviews[i]; q.AsksPage != "" && r.State == "open" && r.Task == q.Task && r.Identity == q.Identity && r.Lavish == q.AsksPage && !r.CreatedAt.After(q.CreatedAt) {
			r.State, r.AnsweredBy, r.AnsweredIn, r.Reason, r.UpdatedAt = "answered", by, "question", bounded(reason, 2000), time.Now().UTC()
		}
	}
}

// closeFollowUps closes, as answered by who at at, every open wait that
// follows question q, which has been answered: the decision was what each
// waited on, so none stays open behind it. The caller holds the store lock.
func (s *Store) closeFollowUps(q Question, who string, at time.Time) {
	reason := "You answered the decision it waited on, " + q.ID + "."
	if who == "cfo" {
		reason = "The CFO answered the decision it waited on, " + q.ID + "."
	}
	for i := range s.db.Reviews {
		if r := &s.db.Reviews[i]; r.State == "open" && r.Follows == q.ID {
			r.State, r.AnsweredBy, r.AnsweredIn, r.Reason, r.UpdatedAt = "answered", who, "question", reason, at
		}
	}
}

// windowClosed records when the captured review's window disconnected.
func (s *Store) windowClosed(item Review, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Reviews, func(r Review) bool { return samePageVersion(r, item) && r.State == "open" })
	if i < 0 {
		return nil
	}
	s.db.Reviews[i].WindowClosedAt = &at
	return s.save()
}

// reviseReview marks the captured open version as waiting on its reporter's next
// version, which the Overlord asked for on its page at at.
func (s *Store) reviseReview(item Review, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Reviews, func(r Review) bool { return samePageVersion(r, item) && r.State == "open" })
	if i < 0 {
		return nil
	}
	s.db.Reviews[i].RevisingSince = &at
	return s.save()
}

// The caller holds the store lock. A retained row never stands for a newer
// publication of the same page, even when that publication has another ID.
func (s *Store) currentPageVersion(item Review) bool {
	isFound := false
	for _, r := range s.db.Reviews {
		if pageKey(r.LavishPage) == pageKey(item.LavishPage) && r.CreatedAt.After(item.CreatedAt) {
			return false
		}
		isFound = isFound || samePageVersion(r, item)
	}
	return isFound
}

func (s *Store) withdrawReview(id, reason string) error {
	return s.closeReview(id, Review{State: "withdrawn", Reason: reason})
}

// closeReview closes the open item id as closed says: its state, its reason
// and who answered it where. An item already closed is left as it is.
func (s *Store) closeReview(id string, closed Review) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Reviews, func(r Review) bool { return r.ID == id && r.State == "open" })
	if i < 0 {
		return nil
	}
	r := &s.db.Reviews[i]
	r.State, r.Reason, r.AnsweredBy, r.AnsweredIn, r.UpdatedAt = closed.State, closed.Reason, closed.AnsweredBy, closed.AnsweredIn, time.Now().UTC()
	return s.save()
}
