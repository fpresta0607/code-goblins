package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// keptFor is how long a check's result is kept across a restart of the
// supervisor: a result older than it is left out when the cache reads its
// file back, since a goblin that ran all that time was checked again since.
const keptFor = 24 * time.Hour

// keptCheck is one key's result as the cache's file keeps it.
type keptCheck struct {
	Result Snapshot  `json:"result"`
	At     time.Time `json:"at"`
	Record string    `json:"record,omitempty"`
}

type cached struct {
	result   Snapshot
	at       time.Time
	checking bool
	pending  bool
	// record is the task's record as the latest Read saw it.
	record string
}
type Cache struct {
	mu             sync.Mutex
	entries        map[string]*cached
	ttl, timeLimit time.Duration
	check          func(context.Context, string) Snapshot
	ctx            context.Context
	cancel         context.CancelFunc
	slots          chan struct{}
	work           sync.WaitGroup
	// Now is the clock results are dated and judged stale by, which a test
	// moves to judge a result's age without waiting it out.
	Now func() time.Time
	// path is the file Keep named, which every finished check is written to,
	// one write at a time; Failed is given a failure to write it.
	path   string
	saving sync.Mutex
	Failed func(error)
}

func NewCache(ttl, timeout time.Duration, check func(context.Context, string) Snapshot) *Cache {
	ctx, cancel := context.WithCancel(context.Background())
	return &Cache{entries: map[string]*cached{}, ttl: ttl, timeLimit: timeout, check: check, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 2), Now: time.Now}
}
func (c *Cache) Get(key string, refresh bool) Snapshot { return c.Read(key, "", refresh) }

// Read is Get for a key whose task's record reads as record now. A record
// that differs from the one the key was last read with, a grant or revoke
// made with goblins auth, starts a check as refresh does, while the result
// read before shows with its age (#2394). An empty record is compared with
// nothing.
func (c *Cache) Read(key, record string, refresh bool) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	item := c.entries[key]
	if item == nil {
		if len(c.entries) >= 128 {
			for oldKey, old := range c.entries {
				if !old.checking {
					delete(c.entries, oldKey)
					break
				}
			}
			if len(c.entries) >= 128 {
				return Snapshot{Entries: []Entry{}, Error: "Connection checks are busy. Try again shortly."}
			}
		}
		item = &cached{result: Snapshot{Entries: []Entry{}}}
		c.entries[key] = item
	}
	if record != "" {
		refresh = refresh || item.record != "" && item.record != record
		item.record = record
	}
	if item.checking && refresh {
		item.pending = true
	}
	if !item.checking && c.ctx.Err() == nil && (refresh || item.at.IsZero() || c.Now().Sub(item.at) >= c.ttl) {
		item.checking = true
		c.work.Add(1)
		go c.run(key, item)
	}
	// An observation older than the time to live keeps its verdicts, each
	// with the time it was checked, while the recheck it started runs: its age
	// says how old it is, and calling it unverified would say a check failed
	// to confirm what no check has looked at again yet.
	return item.snapshot()
}

func (c *Cache) Cached(key string) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if item := c.entries[key]; item != nil {
		return item.snapshot()
	}
	return Snapshot{Entries: []Entry{}}
}

func (item *cached) snapshot() Snapshot {
	result := item.result
	result.Entries = append([]Entry{}, result.Entries...)
	result.Checking = item.checking
	return result
}
func (c *Cache) run(key string, item *cached) {
	defer c.work.Done()
	result := Snapshot{Entries: []Entry{}}
	// A check's time limit starts once it has a slot, so the checks a page
	// asks for at once wait their turn rather than time out unrun (#2394).
	ctx := c.ctx
	select {
	case c.slots <- struct{}{}:
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(c.ctx, c.timeLimit)
		defer cancel()
		finished := make(chan Snapshot, 1)
		go func() { defer func() { <-c.slots }(); finished <- c.check(ctx, key) }()
		select {
		case result = <-finished:
		case <-ctx.Done():
		}
	case <-ctx.Done():
	}
	defer c.save()
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		result = Snapshot{Error: "Connection check timed out.", Entries: append([]Entry{}, item.result.Entries...)}
		for i := range result.Entries {
			result.Entries[i].Status = "unverified"
			result.Entries[i].LiveStatus = "unknown"
			result.Entries[i].Detail = "Latest check timed out."
		}
	}
	result.CheckedAt = c.Now().UTC()
	item.result, item.at, item.checking = result, result.CheckedAt, false
	if item.pending && c.ctx.Err() == nil {
		item.pending, item.checking = false, true
		c.work.Add(1)
		go c.run(key, item)
	}
}
// Keep reads back the results path holds, each with the time it was checked,
// and writes every later result to it, so a restarted supervisor shows each
// goblin's last result at once and rechecks it behind as an old result is
// (#2547). A missing file holds nothing; an unreadable one is an error, and
// is written over by the next result.
func (c *Cache) Keep(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.path = path
	data, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var kept map[string]keptCheck
	if err := json.Unmarshal(data, &kept); err != nil {
		return fmt.Errorf("connection checks kept in %s are unreadable: %w", path, err)
	}
	for key, check := range kept {
		if c.Now().Sub(check.At) >= keptFor || len(c.entries) >= 128 || c.entries[key] != nil {
			continue
		}
		check.Result.Checking = false
		c.entries[key] = &cached{result: check.Result, at: check.At, record: check.Record}
	}
	return nil
}

// save writes every finished result to the file Keep named.
func (c *Cache) save() {
	c.saving.Lock()
	defer c.saving.Unlock()
	c.mu.Lock()
	// A check the supervisor's close cut short proved nothing, so the file
	// keeps the result before it for the next supervisor.
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		return
	}
	path, failed := c.path, c.Failed
	kept := map[string]keptCheck{}
	for key, item := range c.entries {
		if !item.at.IsZero() {
			kept[key] = keptCheck{Result: item.result, At: item.at, Record: item.record}
		}
	}
	c.mu.Unlock()
	if path == "" {
		return
	}
	data, err := json.Marshal(kept)
	if err == nil {
		err = fsx.AtomicWriteFile(path, data)
	}
	if err != nil && failed != nil {
		failed(fmt.Errorf("connection checks could not be kept in %s: %w", path, err))
	}
}

func (c *Cache) Close() {
	c.mu.Lock()
	c.cancel()
	c.mu.Unlock()
	c.work.Wait()
}
