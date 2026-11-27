package main

import (
	"os"
	"sync"
	"time"
)

// fileMemo keeps one answer per id, good while the file it was computed from keeps its size and
// modification time: the page asks again on every refresh, and a run log that has not changed
// has the same answer. The zero value is ready to use.
type fileMemo[V any] struct {
	mu sync.Mutex
	m  map[string]memoEntry[V]
}

type memoEntry[V any] struct {
	size int64
	mod  time.Time
	val  V
}

// fresh says whether the entry was computed from the file as it is now.
func (e memoEntry[V]) fresh(st os.FileInfo) bool {
	return e.size == st.Size() && e.mod.Equal(st.ModTime())
}

// get is the entry of id, or the zero entry (never fresh for a real file) when there is none.
func (c *fileMemo[V]) get(id string) memoEntry[V] {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[id]
}

func (c *fileMemo[V]) put(id string, st os.FileInfo, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]memoEntry[V]{}
	}
	c.m[id] = memoEntry[V]{size: st.Size(), mod: st.ModTime(), val: v}
}

// prune forgets every id keep refuses.
func (c *fileMemo[V]) prune(keep func(id string) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.m {
		if !keep(id) {
			delete(c.m, id)
		}
	}
}
