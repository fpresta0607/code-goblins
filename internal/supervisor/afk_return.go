package supervisor

import (
	"bytes"
	"cmp"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/afk"
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

// afkFaces are the board's pictures of its goblins the return page wears.
var afkFaces = []string{"goblin-personas.png", "goblin-specialists.png", "goblin-specialists-2.png", "goblin-app.png"}

// makeReturnPage writes the page a stretch of AFK mode ends with into its own
// folder under the state directory: the report as the walkthrough he reads,
// each decision with the question in his History it changes, each picture a
// goblin showed of its work in the stretch, and the board's goblin faces. It
// returns the page's file.
func (s *Service) makeReturnPage(report afk.Report) (string, error) {
	root := filepath.Join(s.Store.Home.State, afkReturnDir)
	dir := filepath.Join(root, report.Session)
	d := s.Store.Snapshot()
	// Keep only the last stretch's report.
	earlier, _ := os.ReadDir(root)
	for _, entry := range earlier {
		if entry.Name() != report.Session {
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
		r.Questions["question:"+q.ID] = afk.Question{ID: q.ID, Text: q.Text, Answer: q.Answer}
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
