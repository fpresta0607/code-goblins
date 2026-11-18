package fsx

// CommitUnheld is Commit: a reader that opened the file without sharing
// deletion already holds the rename off until it lets go, and Commit waits
// it out within transientBudget.
func (s *Staged) CommitUnheld() error {
	return s.Commit()
}
