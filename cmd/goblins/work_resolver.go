package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/workreview"
)

// workResolver names the repository and checkout of the project a backlog row
// or a task record names, for the work review. It reads the repository of a
// checkout that is there once, and what it could not read is its error, to be
// read again at the next call, as is a checkout not there yet. It is safe to
// call from several goroutines.
func workResolver(ctx context.Context, h home.Home, resolveProject func(string) (string, error), repositoryOf func(context.Context, string) (string, error)) workreview.Resolver {
	var mu sync.Mutex
	repositories := map[string]string{}
	return func(project string) (string, string, error) {
		if project == "" {
			return "", "", nil
		}
		mu.Lock()
		defer mu.Unlock()
		checkout, err := resolveProject(project)
		if err != nil {
			return "", "", fmt.Errorf("project %s is not read: %w", project, err)
		}
		key := strings.ToLower(filepath.Clean(checkout))
		if repository, isKnown := repositories[key]; isKnown {
			return repository, checkout, nil
		}
		// A checkout not there yet has no repository until it is.
		if _, err := os.Stat(checkout); errors.Is(err, fs.ErrNotExist) {
			return "", checkout, nil
		} else if err != nil {
			return "", "", fmt.Errorf("the checkout %s cannot be read: %w", checkout, err)
		}
		repository, err := issueRepository(ctx, h, repositoryOf, checkout)
		if err != nil {
			return "", "", fmt.Errorf("the repository of %s cannot be read: %w", checkout, err)
		}
		repositories[key] = repository
		return repository, checkout, nil
	}
}
