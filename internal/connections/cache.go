package connections

import (
	"context"
	"sync"
	"time"
)

type cached struct {
	result   Snapshot
	at       time.Time
	checking bool
	pending  bool
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
}

func NewCache(ttl, timeout time.Duration, check func(context.Context, string) Snapshot) *Cache {
	ctx, cancel := context.WithCancel(context.Background())
	return &Cache{entries: map[string]*cached{}, ttl: ttl, timeLimit: timeout, check: check, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 2)}
}
func (c *Cache) Get(key string, refresh bool) Snapshot {
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
	if item.checking && refresh {
		item.pending = true
	}
	if !item.checking && c.ctx.Err() == nil && (refresh || item.at.IsZero() || time.Since(item.at) >= c.ttl) {
		item.checking = true
		c.work.Add(1)
		go c.run(key, item)
	}
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
	ctx, cancel := context.WithTimeout(c.ctx, c.timeLimit)
	defer cancel()
	result := Snapshot{Entries: []Entry{}}
	select {
	case c.slots <- struct{}{}:
		finished := make(chan Snapshot, 1)
		go func() { defer func() { <-c.slots }(); finished <- c.check(ctx, key) }()
		select {
		case result = <-finished:
		case <-ctx.Done():
		}
	case <-ctx.Done():
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		result = Snapshot{Error: "Connection check timed out.", Entries: append([]Entry{}, item.result.Entries...)}
		for i := range result.Entries {
			result.Entries[i].Status = "unverified"
			result.Entries[i].Detail = "Latest check timed out."
		}
	}
	result.CheckedAt = time.Now().UTC()
	item.result, item.at, item.checking = result, result.CheckedAt, false
	if item.pending && c.ctx.Err() == nil {
		item.pending, item.checking = false, true
		c.work.Add(1)
		go c.run(key, item)
	}
}
func (c *Cache) Close() {
	c.mu.Lock()
	c.cancel()
	c.mu.Unlock()
	c.work.Wait()
}
