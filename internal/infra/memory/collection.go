// Package memory keeps the mock's state in process.
//
// A fake has no reason to reach for a database: fixtures load at boot, tests
// reset between cases, and nothing has to survive a restart. The domain ports
// are still honoured, so swapping in a Postgres adapter later touches only this
// package.
package memory

import (
	"sort"
	"sync"
)

// identifiable is anything the collection can key by. Every incident.io
// resource carries a ULID in an `Id` field, so the accessor is supplied per
// collection rather than through an interface the generated types cannot
// implement.
type keyFunc[T any] func(T) string

// collection is a goroutine-safe, ID-keyed set of records.
//
// Records are returned in ID order, which is what the `after` cursor pages
// through. For records the mock mints that is also chronological order, because
// their IDs are ULIDs. Fixtures may use readable handles like "INC-CHECKOUT"
// instead, which sort wherever they sort — the ordering stays stable and
// total, so pagination is still correct, but it is not a timeline.
type collection[T any] struct {
	mu   sync.RWMutex
	key  keyFunc[T]
	byID map[string]T
}

func newCollection[T any](key keyFunc[T]) *collection[T] {
	return &collection[T]{key: key, byID: map[string]T{}}
}

func (c *collection[T]) put(item T) T {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.byID[c.key(item)] = item

	return item
}

func (c *collection[T]) get(id string) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, ok := c.byID[id]

	return item, ok
}

// update applies mutate to a copy of the stored record and writes it back, so a
// failed mutation cannot leave a half-changed record behind.
func (c *collection[T]) update(id string, mutate func(*T) error) (T, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, ok := c.byID[id]
	if !ok {
		var zero T

		return zero, false, nil
	}

	draft := item
	if err := mutate(&draft); err != nil {
		var zero T

		return zero, true, err
	}

	c.byID[id] = draft

	return draft, true, nil
}

func (c *collection[T]) delete(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.byID[id]; !ok {
		return false
	}

	delete(c.byID, id)

	return true
}

// all returns every record in ID order.
func (c *collection[T]) all() []T {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := make([]string, 0, len(c.byID))
	for id := range c.byID {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	out := make([]T, 0, len(ids))
	for _, id := range ids {
		out = append(out, c.byID[id])
	}

	return out
}

func (c *collection[T]) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.byID = map[string]T{}
}

func (c *collection[T]) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.byID)
}
