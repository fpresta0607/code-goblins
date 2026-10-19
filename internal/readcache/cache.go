// Package readcache answers the reads the board repeats, git's and the gate
// database's, from what the same read last returned while the files it
// reads are unchanged, so a board refresh over repositories and a database
// nobody changed starts no process.
//
// Every process start makes the kernel a token, and on 2026-10-19 about two
// in five of them outlived their process for good, which grew the kernel's
// paged pool until the PC had to be restarted. The board started git about
// 5,000 times an hour then, most of it to read the same heads, branches and
// refs every minute, each through Git for Windows' launcher and a console
// host: three processes a read.
package readcache

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// maxAnswers bounds how many answers the cache keeps: far more than the
// reads one board repeats, and all of them are dropped when it fills.
const maxAnswers = 4096

// Cache keeps the last answer of each read it knows. Its zero value is not
// ready; New makes one, and a nil Cache answers nothing and runs every read.
type Cache struct {
	mu      sync.Mutex
	answers map[string]answer
	config  []string
	now     func() time.Time
}

// answer is what a read returned, and the state of the files it depends on
// as they were before it ran. An answer read while those files were being
// written is kept unsettled: it never answers a read, only tells the next
// one what git ignores in a worktree.
type answer struct {
	stamp     [32]byte
	isSettled bool
	result    execx.Result
}

// New is an empty cache for this process's environment: the git
// configuration files every git read depends on are found once.
func New() *Cache {
	return &Cache{answers: map[string]answer{}, config: gitConfigFiles(), now: time.Now}
}

// Read answers req from the cache when it is a read the cache knows and the
// files it depends on are as they were when it last ran; otherwise it runs
// req with run, starting a read that starts no other program with no
// console, and keeps the answer. A failure, and an exit code that is not
// the read's answer, is returned and never kept.
func (c *Cache) Read(req execx.Request, run func(execx.Request) (execx.Result, error)) (execx.Result, error) {
	if c == nil {
		return run(req)
	}
	read, ok := planOf(req)
	if !ok {
		return run(req)
	}
	req.NoConsole = read.isSilent
	c.mu.Lock()
	last, isKnown := c.answers[read.key]
	c.mu.Unlock()
	var previous *execx.Result
	if isKnown {
		previous = &last.result
	}
	s := newStamper(c.now())
	read.stamp(s, c.config, previous)
	stamp, isSettled := s.sum()
	if isSettled && isKnown && last.isSettled && last.stamp == stamp {
		return copyResult(last.result), nil
	}
	result, err := run(req)
	if err != nil || !slices.Contains(read.answers, result.ExitCode) {
		return result, err
	}
	c.mu.Lock()
	if len(c.answers) >= maxAnswers {
		clear(c.answers)
	}
	c.answers[read.key] = answer{stamp: stamp, isSettled: isSettled, result: copyResult(result)}
	c.mu.Unlock()
	return result, nil
}

// Runner is Next with the cache in front of it.
type Runner struct {
	Cache *Cache
	Next  execx.Runner
}

func (r Runner) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	return r.Cache.Read(req, func(req execx.Request) (execx.Result, error) { return r.Next.Run(ctx, req) })
}

func copyResult(result execx.Result) execx.Result {
	return execx.Result{Stdout: bytes.Clone(result.Stdout), Stderr: bytes.Clone(result.Stderr), ExitCode: result.ExitCode}
}
