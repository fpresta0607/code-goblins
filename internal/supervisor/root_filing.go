package supervisor

import (
	"context"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/tickets"
)

const (
	// rootFilingWithin bounds one filing: a read of the open root issues
	// and a write.
	rootFilingWithin = time.Minute
	// errorRepeatsAfter is how long after the supervisor first met a class
	// of error meeting it again makes it a root issue: a failure that
	// clears within the hour, such as GitHub answering slowly once, is not.
	errorRepeatsAfter = time.Hour
)

// fileFoundRoot files root on the repository of project, a name or a path,
// away from the loop that found it, through the same path and screen as cfo
// issue file. A filing that fails tells the CFO once, as a wake keyed by the
// root, and never goes back through publish, where it would be an error
// that files itself.
func (s *Service) fileFoundRoot(project string, root tickets.Root) {
	file := s.Options.FileRoot
	if file == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rootFilingWithin)
		defer cancel()
		if _, err := file(ctx, project, root); err != nil {
			detail := "root_unfiled: " + bounded(root.Title, 200) + " was not filed: " + bounded(err.Error(), 400) + "; next: file it with cfo issue file, written for its repository"
			if err := raiseFleetWake(s.Store.Home.State, "check", "root:"+bounded(root.RootKey(), 100), detail); err != nil {
				s.publish(err)
			}
		}
	}()
}

// fileRepeatedErrors files a root issue on the home's own repository for
// each class of the lines of err that the supervisor first met at least
// errorRepeatsAfter ago, once a class in each run of the supervisor: the
// issue's own dedup turns a later run's filing into a dated comment. A line
// that says the network could not be reached is passed over.
func (s *Service) fileRepeatedErrors(err error, now time.Time) {
	if err == nil || s.Options.FileRoot == nil {
		return
	}
	var due []tickets.Root
	s.mu.Lock()
	if s.errorClassesMet == nil {
		s.errorClassesMet, s.errorClassesFiled = map[string]time.Time{}, map[string]bool{}
	}
	for _, line := range strings.Split(err.Error(), "\n") {
		class := tickets.ErrorClass(line)
		if class == "" || tickets.IsLostConnection(line) {
			continue
		}
		first, isMet := s.errorClassesMet[class]
		switch {
		case !isMet:
			s.errorClassesMet[class] = now
		case now.Sub(first) >= errorRepeatsAfter && !s.errorClassesFiled[class]:
			s.errorClassesFiled[class] = true
			due = append(due, tickets.RepeatedErrorRoot(class, "First met "+first.UTC().Format("2006-01-02 15:04Z")+", met again "+now.UTC().Format("2006-01-02 15:04Z")+"."))
		}
	}
	s.mu.Unlock()
	for _, root := range due {
		s.fileFoundRoot(s.Store.Home.Root, root)
	}
}
