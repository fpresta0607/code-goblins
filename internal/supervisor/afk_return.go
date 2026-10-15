package supervisor

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// afkNextRows bounds how many queued tasks the return page names as next.
const afkNextRows = 5

// taskNaming is a task's title, the CFO's one line for the work, and its
// goblin's name, from its live record, else what it left when it finished,
// else its backlog row. A task none of them names is called by its ID.
func (s *Service) taskNaming(id string) (title, goblin string) {
	if meta, err := s.taskMeta(id); err == nil {
		title, goblin = meta.Title, meta.GoblinName
	}
	if title == "" {
		if outcome, err := state.ReadOutcome(s.Store.Home.State, id); err == nil {
			title, goblin = outcome.Title, cmp.Or(goblin, outcome.GoblinName)
		}
	}
	if title == "" {
		if record, err := state.ReadLifecycle(s.Store.Home.State, id); err == nil {
			title, goblin = record.Title, cmp.Or(goblin, record.GoblinName)
		}
	}
	if title == "" {
		if backlog, err := s.backlog(); err == nil {
			for _, row := range slices.Concat(backlog.Queued, backlog.Done, backlog.Parked) {
				if row.Structured && row.ID == id {
					title = row.Title
					break
				}
			}
		}
	}
	return cmp.Or(title, id), goblin
}

// namedFinishes is each finished pull request with its task's title and its
// goblin's name.
func (s *Service) namedFinishes(finished []afk.Finish) []afk.Finish {
	named := slices.Clone(finished)
	for i := range named {
		named[i].Title, named[i].Goblin = s.taskNaming(named[i].Task)
	}
	return named
}

// afkUnderWay is each goblin's work still under way, with its latest note,
// and the first tasks queued to start, with what each waits for.
func (s *Service) afkUnderWay() (underWay, next []afk.Ongoing) {
	entries, _ := os.ReadDir(s.Store.Home.State)
	for _, entry := range entries {
		id, isMeta := strings.CutSuffix(entry.Name(), ".meta")
		if entry.IsDir() || !isMeta || state.ValidTaskID(id) != nil {
			continue
		}
		meta, err := s.taskMeta(id)
		if err != nil || meta.Parent != "" {
			continue
		}
		lines, _ := s.statusTail(id)
		at, said := standingReport(lines, spawnTime(meta.SpawnGen))
		title, goblin := s.taskNaming(id)
		underWay = append(underWay, afk.Ongoing{Task: id, Title: title, Goblin: goblin, Note: bounded(said, afkNoteLength), At: at})
	}
	if backlog, err := s.backlog(); err == nil {
		for _, row := range backlog.Queued {
			if !row.Structured || slices.ContainsFunc(underWay, func(one afk.Ongoing) bool { return one.Task == row.ID }) {
				continue
			}
			if next = append(next, afk.Ongoing{Task: row.ID, Title: row.Title, Note: row.BlockedReason}); len(next) == afkNextRows {
				break
			}
		}
	}
	return underWay, next
}

// afkReturnDir is where the supervisor keeps the page each stretch of AFK
// mode ends with, under the state directory, one folder for each stretch.
const afkReturnDir = "afk-return"

// isReturnPage reports whether an item is the page a stretch of AFK mode
// ended with: the supervisor's own, kept in its own folder. Only that page's
// Change reaches the Overlord's answers, so no goblin's page can change them.
func (s *Service) isReturnPage(r Review) bool {
	return r.Task == "" && r.LavishPage != "" && filepath.Dir(filepath.Dir(r.LavishPage)) == filepath.Join(s.Store.Home.State, afkReturnDir)
}

// changeOnPage sends each answer the Overlord changed on the return page as
// the change History sends: an answer_change for that question, with his
// choice, or with his own words for an answer none of its choices is. It
// says what became of each, for the page's conversation.
func (s *Service) changeOnPage(changes []axi.PageChange) string {
	questions := s.Store.Snapshot().Questions
	var says []string
	for _, change := range changes {
		i := slices.IndexFunc(questions, func(q Question) bool { return q.ID == change.Question })
		if i < 0 {
			says = append(says, "That question is no longer in your History, so nothing changed.")
			continue
		}
		q := questions[i]
		kind := "other"
		if slices.Contains(q.Options, change.Answer) {
			kind = "option"
		}
		if _, err := s.Store.Queue(Action{ID: "page-change-" + rand.Text(), Kind: "answer_change", Generation: q.Identity, QuestionID: q.ID, Text: change.Answer, AnswerKind: kind}); err != nil {
			says = append(says, "Not changed: "+bounded(err.Error(), 300))
			continue
		}
		says = append(says, "Changed to "+strconv.Quote(change.Answer)+". It reaches whoever acted on it once, as a change.")
	}
	s.publish(nil)
	return strings.Join(says, "\n")
}

// afkPageOpenTimeout bounds opening the return page in Scrawl.
const afkPageOpenTimeout = time.Minute

// afkFaces are the board's pictures of its goblins the return page wears.
var afkFaces = []string{"goblin-personas.png", "goblin-specialists.png", "goblin-specialists-2.png", "goblin-app.png"}

// returnPageID names the Command Center item of a stretch's return page.
func returnPageID(session string) string {
	return "afk-return-" + strings.TrimPrefix(session, "afk-")
}

// makeReturnPage writes the page a stretch of AFK mode ends with into its own
// folder under the state directory: the report as the walkthrough he reads,
// each decision with the question in his History it changes, each picture a
// goblin showed of its work in the stretch, and the board's goblin faces. It
// returns the page's file.
func (s *Service) makeReturnPage(report afk.Report) (string, error) {
	root := filepath.Join(s.Store.Home.State, afkReturnDir)
	dir := filepath.Join(root, report.Session)
	d := s.Store.Snapshot()
	// An earlier stretch's page goes once its card no longer waits on him,
	// so pages never pile up.
	earlier, _ := os.ReadDir(root)
	for _, entry := range earlier {
		page := filepath.Join(root, entry.Name(), "index.html")
		if entry.Name() != report.Session && !slices.ContainsFunc(d.Reviews, func(item Review) bool { return item.LavishPage == page && item.State == "open" }) {
			if err := fsx.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return "", err
			}
		}
	}
	if err := fsx.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(dir, "pictures"), 0o700); err != nil {
		return "", err
	}
	r := afk.Return{Report: report, Questions: map[string]afk.Question{}, Titles: map[string]string{}}
	for _, q := range d.Questions {
		r.Questions["question:"+q.ID] = afk.Question{ID: q.ID, Text: q.Text, Options: slices.Clone(q.Options), Answer: q.Answer}
	}
	s.mu.Lock()
	for _, task := range s.history {
		if task.PR != "" && !strings.HasPrefix(task.ID, "merged:") {
			r.Titles[task.PR] = task.Title
		}
	}
	s.mu.Unlock()
	for _, item := range d.Reviews {
		if item.Task == "" || len(item.ImageSums) == 0 || item.CreatedAt.Before(report.Since) || item.CreatedAt.After(report.Ended) {
			continue
		}
		source := reviewImageDir(s.Store.Home.State, item)
		for n := range item.ImageSums {
			picture, err := copyPicture(source, n, filepath.Join(dir, "pictures"), item.ID)
			if err != nil {
				continue
			}
			caption := item.Title
			if n > 0 {
				caption += " " + strconv.Itoa(n+1)
			}
			r.Pictures = append(r.Pictures, afk.Picture{Task: item.Task, Caption: caption, File: "pictures/" + picture})
		}
	}
	if s.Options.BoardAssets != nil {
		for _, face := range afkFaces {
			if data, err := fs.ReadFile(s.Options.BoardAssets, "assets/"+face); err == nil {
				if err := os.WriteFile(filepath.Join(dir, face), data, 0o600); err != nil {
					return "", err
				}
			}
		}
	}
	var page bytes.Buffer
	if err := afk.RenderPage(&page, r); err != nil {
		return "", err
	}
	file := filepath.Join(dir, "index.html")
	return file, fsx.AtomicWriteFile(file, page.Bytes())
}

// copyPicture copies a goblin's picture n of an item beside the return page,
// named for the item and its kind of image, and returns its name.
func copyPicture(source string, n int, dir, item string) (string, error) {
	in, kind, err := openReviewImage([]string{source}, filepath.Join(source, strconv.Itoa(n)))
	if err != nil {
		return "", err
	}
	defer in.Close()
	extension := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}[kind]
	if extension == "" {
		return "", errors.New("not a picture the page shows")
	}
	name := item + "-" + strconv.Itoa(n) + extension
	out, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(out, io.LimitReader(in, maxReviewImage))
	return name, errors.Join(copyErr, out.Close())
}

// presentReturnPage opens the return page in Scrawl and puts its one card in
// the Command Center, as the CFO's own item, which the supervisor then
// watches like any page. Opening runs lavish-axi, so it runs beside the
// switch, which waits on no program, and the CFO hears if it fails.
func (s *Service) presentReturnPage(session, file string) {
	if s.Options.OpenPage == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), afkPageOpenTimeout)
	defer cancel()
	link, err := s.Options.OpenPage(ctx, file)
	if err == nil {
		var identity string
		if _, identity, err = readPrimary(s.Store.Home.State); err == nil {
			now := time.Now().UTC()
			err = s.Store.acceptReview(Review{ID: returnPageID(session), Identity: identity, Title: "Welcome back: what happened while you were away", Lavish: link, LavishPage: file, State: "open", CreatedAt: now, UpdatedAt: now})
		}
	}
	if err != nil {
		s.publish(s.afkNotice("His page of the stretch, " + file + ", could not be put in his Command Center (" + bounded(err.Error(), 300) + "). Tell him what it holds in your reply: cfo afk report prints it."))
		return
	}
	s.notify()
}
