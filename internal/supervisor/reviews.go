package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// maxReviews bounds the review list. An open item is never dropped to make
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
	Task     string `json:"task,omitempty"`
	Title    string `json:"title"`
	// ImageSums are the SHA-256 of each copied image in order. They make a
	// republish with the same ID and content change nothing and refuse other
	// content, and the board only ever sees ImageCount.
	ImageSums  []string `json:"image_sums,omitempty"`
	ImageCount int      `json:"image_count,omitempty"`
	Lavish     string   `json:"lavish,omitempty"`
	State      string   `json:"state"`
	Answer     string   `json:"answer,omitempty"`
	AnswerID   string   `json:"answer_id,omitempty"`
	// Delivered says the answer reached the reporter itself; an answer for
	// a goblin that restarted or ended goes to the CFO and stays false.
	Delivered bool      `json:"delivered,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
		if problem := presentationURLProblem(r.Lavish); problem != "" {
			return errors.New("a review's Lavish link " + problem)
		}
	}
	return nil
}

func sameReview(a, b Review) bool {
	return a.Identity == b.Identity && a.Task == b.Task && a.Title == b.Title && a.Lavish == b.Lavish && slices.Equal(a.ImageSums, b.ImageSums)
}

// reviewImageDir holds one publication's copied images, named by position.
// Its name is that publication's own digest, so no other record, not even a
// republish of the same ID, ever names it.
func reviewImageDir(stateDir string, r Review) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{r.ID, r.Identity, strconv.FormatInt(r.CreatedAt.UnixNano(), 10), strings.Join(r.ImageSums, ",")}, "\n")))
	return filepath.Join(stateDir, "reviews", hex.EncodeToString(sum[:]))
}

// reviewReporter proves who is reporting: a goblin from inside its task's own
// Herdr pane, or the registered primary CFO when taskID is empty. The returned
// release keeps the CFO's registration from changing until the report is made.
func reviewReporter(ctx context.Context, h home.Home, client *herdr.Client, taskID string) (string, func(), error) {
	if taskID != "" {
		meta, err := goblinAsker(ctx, h.State, client, taskID)
		if err != nil {
			return "", nil, err
		}
		return goblinIdentity(meta), func() {}, nil
	}
	return (&CFOConnection{State: h.State, Herdr: client}).CallerIdentity(ctx)
}

// PublishReview reports an item for the Overlord from the reporter's own
// process. Images are checked the way a question's are and copied under
// state/reviews before the item is recorded.
func PublishReview(ctx context.Context, h home.Home, client *herdr.Client, taskID, id, title, lavish string, images []string) error {
	identity, release, err := reviewReporter(ctx, h, client, taskID)
	if err != nil {
		return err
	}
	defer release()
	if len(images) > maxReviewImages || len(images) > 0 && taskID == "" {
		return fmt.Errorf("only a goblin's review takes images, at most %d", maxReviewImages)
	}
	now := time.Now().UTC()
	r := Review{ID: id, Identity: identity, Task: taskID, Title: title, Lavish: lavish, State: "open", CreatedAt: now, UpdatedAt: now}
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
	staged := filepath.Join(h.State, "reviews", ".staged")
	if err := os.RemoveAll(staged); err != nil {
		return err
	}
	defer os.RemoveAll(staged)
	if r.ImageSums, err = stageReviewImages(h, taskID, images, staged); err != nil {
		return err
	}
	prior, found, err := reportedReview(h.State, id)
	if err != nil {
		return err
	}
	if found {
		if !sameReview(prior, r) {
			return errors.New("review ID already used")
		}
		return nil
	}
	if len(images) == 0 {
		return spoolReview(h.State, r)
	}
	final := reviewImageDir(h.State, r)
	if err := os.Rename(staged, final); err != nil {
		return err
	}
	if err := spoolReview(h.State, r); err != nil {
		return errors.Join(err, os.RemoveAll(final))
	}
	return nil
}

// WithdrawReview closes the reporter's own open item with its reason.
func WithdrawReview(ctx context.Context, h home.Home, client *herdr.Client, taskID, id, reason string) error {
	identity, release, err := reviewReporter(ctx, h, client, taskID)
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
	return spoolReview(h.State, Review{ID: id, Identity: identity, Task: taskID, State: "withdrawn", Reason: reason, UpdatedAt: time.Now().UTC()})
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

// reportedReview finds a review waiting in the inbox or already recorded. It
// only reads: opening a Store here would run crash recovery under a live
// supervisor. The inbox is read first because ingest records an item before it
// removes the inbox copy, so one of the two reads always sees it.
func reportedReview(stateDir, id string) (Review, bool, error) {
	data, err := os.ReadFile(reviewInboxPath(stateDir, id, "open"))
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
		data, err := os.ReadFile(filepath.Join(stateDir, ".supervisor.json"))
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
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if json.Unmarshal(data, &r) != nil {
				invalid = errors.New("invalid review JSON")
			}
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
		if err := validReview(r); err != nil {
			return err
		}
		if i >= 0 {
			// A publication that raced one already recorded leaves nothing.
			if dir := reviewImageDir(s.Home.State, r); dir != reviewImageDir(s.Home.State, s.db.Reviews[i]) {
				if err := os.RemoveAll(dir); err != nil {
					return err
				}
			}
			if !sameReview(s.db.Reviews[i], r) {
				return errors.New("review ID already used")
			}
			return nil
		}
		if len(s.db.Reviews) >= maxReviews {
			closed := slices.IndexFunc(s.db.Reviews, func(old Review) bool { return old.State != "open" && !s.answering(old) })
			if closed < 0 {
				return ErrDeferred
			}
			if err := os.RemoveAll(reviewImageDir(s.Home.State, s.db.Reviews[closed])); err != nil {
				return err
			}
			s.db.Reviews = slices.Delete(s.db.Reviews, closed, closed+1)
		}
		r.ImageCount, r.Reason, r.Answer, r.AnswerID, r.Delivered = 0, "", "", "", false
		s.db.Reviews = append(s.db.Reviews, r)
	case "withdrawn":
		if i < 0 {
			// Its publication still waits in the inbox while 128 are open.
			if _, err := os.Stat(reviewInboxPath(s.Home.State, r.ID, "open")); err == nil {
				return ErrDeferred
			}
			return errors.New("no review with that ID to withdraw")
		}
		prior := &s.db.Reviews[i]
		if prior.Identity != r.Identity {
			return errors.New("only the reporter that published a review can withdraw it")
		}
		if prior.State != "open" {
			return nil
		}
		prior.State, prior.Reason, prior.UpdatedAt = "withdrawn", bounded(r.Reason, 2000), r.UpdatedAt
	default:
		return errors.New("a review report is open or withdrawn")
	}
	return s.save()
}

// answering reports whether an item's answer is still queued or running; the
// item keeps its place until the answer is delivered or refused.
func (s *Store) answering(r Review) bool {
	return r.AnswerID != "" && slices.ContainsFunc(s.db.Actions, func(a Action) bool {
		return a.ID == r.AnswerID && (a.Status == "queued" || a.Status == "running")
	})
}

// pruneReviews drops closed items, with their copied images, a set time after
// they closed. Open items and items whose answer is on its way are never
// pruned.
func (s *Store) pruneReviews(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]Review, 0, len(s.db.Reviews))
	var removed []Review
	for _, r := range s.db.Reviews {
		if r.State != "open" && !s.answering(r) && now.Sub(r.UpdatedAt) > closedReviewRetention {
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
		err = errors.Join(err, os.RemoveAll(reviewImageDir(s.Home.State, r)))
	}
	return err
}

// clearReview closes an open item the Overlord cleared. Clearing one already
// closed changes nothing.
func (s *Store) clearReview(id, identity string) (Evaluation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Reviews, func(r Review) bool { return r.ID == id && r.Identity == identity })
	if i < 0 {
		return Evaluation{}, fmt.Errorf("%w: the review is gone; nothing was cleared", ErrRejected)
	}
	if s.db.Reviews[i].State != "open" {
		return Evaluation{Reason: "The review was already " + s.db.Reviews[i].State + "."}, nil
	}
	s.db.Reviews[i].State, s.db.Reviews[i].UpdatedAt = "cleared", time.Now().UTC()
	if err := s.save(); err != nil {
		return Evaluation{}, err
	}
	return Evaluation{Reason: "Cleared from the Command Center."}, nil
}

// answerReview delivers the Overlord's answer once to the item's reporter:
// the goblin's own pane while it is the same task generation, or the CFO that
// reported it. An answer for a goblin that restarted or ended goes to the
// current CFO instead, and the item stays undelivered.
func (s *Service) answerReview(ctx context.Context, a Action) (Evaluation, error) {
	if s.Options.CFO == nil {
		return Evaluation{}, fmt.Errorf("%w: Herdr message transport is unavailable", ErrRejected)
	}
	reviews := s.Store.Snapshot().Reviews
	i := slices.IndexFunc(reviews, func(r Review) bool { return r.ID == a.ReviewID && r.Identity == a.Generation && r.AnswerID == a.ID })
	if i < 0 {
		return Evaluation{}, fmt.Errorf("%w: the review changed; nothing was sent", ErrRejected)
	}
	r := reviews[i]
	var result Evaluation
	var err error
	if r.Task == "" {
		result, err = s.Options.CFO.Send(ctx, r.Identity, fmt.Sprintf("Answer to your review item %s (%s): %s", r.ID, r.Title, a.Text))
	} else {
		result, err = s.Options.CFO.SendGoblin(ctx, r.Task, r.Identity, fmt.Sprintf("The Overlord answered your review item %s (%s): %s", r.ID, r.Title, a.Text))
		if errors.Is(err, ErrRejected) {
			return s.answerReviewToCFO(ctx, r, a.Text)
		}
	}
	if err != nil {
		return result, err
	}
	return result, s.Store.markReviewDelivered(r.ID, a.ID)
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
// request.
func (h *HTTP) reviewImage(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/reviews/"), "/")
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
// item for him until he answers or clears it, or the goblin reports again. It
// names the item waiting-<task>-<wake sequence>, which retireWaits relies on.
func PublishWait(ctx context.Context, h home.Home, client *herdr.Client, taskID string, seq int, why string) error {
	return PublishReview(ctx, h, client, taskID, fmt.Sprintf("waiting-%s-%d", taskID, seq), "Waiting on you: "+why, "", nil)
}

// retireWaits withdraws a goblin's wait on the Overlord once its task reports
// anything newer than that wait, or is gone, so the Command Center never keeps
// a request nobody is waiting on.
func (s *Store) retireWaits() error {
	for _, r := range s.Snapshot().Reviews {
		if r.State != "open" || r.Task == "" || !strings.HasPrefix(r.ID, "waiting-"+r.Task+"-") {
			continue
		}
		lines, err := state.TailStatus(s.Home.State, r.Task, 50)
		if err != nil {
			return err
		}
		reportedAt, report := latestReport(lines, time.Time{})
		if strings.HasPrefix(report, "waiting on overlord: ") && !reportedAt.After(r.CreatedAt) {
			continue
		}
		reason := r.Task + " reported again: " + report
		if report == "" {
			reason = r.Task + " is gone"
		}
		if err := s.withdrawReview(r.ID, bounded(reason, 2000)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) withdrawReview(id, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.db.Reviews, func(r Review) bool { return r.ID == id && r.State == "open" })
	if i < 0 {
		return nil
	}
	s.db.Reviews[i].State, s.db.Reviews[i].Reason, s.db.Reviews[i].UpdatedAt = "withdrawn", reason, time.Now().UTC()
	return s.save()
}
