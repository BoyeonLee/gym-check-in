// Package cache contains the in-process LRU used as the second-layer
// idempotency guard for kiosk check-ins.
//
// Background — backend/CLAUDE.md "이중 클릭 방지(짧은 멱등성)":
//
//	(member_id, branch_id) 기준으로 직전 5초 안에 성공한 체크인이 있으면,
//	새 row를 만들지 않고 기존 체크인 응답을 그대로 반환(같은 클릭의 중복 처리).
//
// IMPORTANT: This LRU is process-local. MVP assumes a single backend
// instance (root CLAUDE.md). When the deployment is scaled out (multi
// instance / blue-green) this layer must move to Redis or be replaced
// by a request-coalescing proxy — otherwise concurrent kiosks across
// instances may produce duplicate check_ins rows in the 5s window.
//
// Design:
//   - Size cap (maxEntries) drives a doubly-linked-list eviction order.
//   - TTL is enforced lazily: Get on an expired entry removes it and
//     reports miss. A background sweeper would be overkill at MVP scale.
//   - Clock is injected so tests don't sleep — they FreezeTime / advance.
package cache

import (
	"container/list"
	"sync"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// CheckInResult is the value stored per cache key. Body / Status replay
// the exact JSON the previous request received; ID / CheckedInAt are
// kept for log correlation and tests that want to assert "the same row
// won the race".
type CheckInResult struct {
	ID          int64
	CheckedInAt time.Time
	Body        []byte
	Status      int
}

// entry is the linked-list payload — keep both key and value so eviction
// can remove the map entry without a reverse lookup.
type entry struct {
	key       string
	value     CheckInResult
	expiresAt time.Time
}

// LRU is a fixed-capacity, TTL-bounded cache safe for concurrent use.
//
// Methods:
//
//	Get(key) — returns (value, true) when the entry exists and has not
//	  expired. Expired entries are evicted on this read so the next caller
//	  doesn't pay the cost.
//	Set(key, v) — inserts or refreshes the entry; updates LRU recency.
//
// Set is also "Update" — calling Set on an existing key replaces the
// value and resets the TTL window. The kiosk handler doesn't rely on
// this but it makes test ergonomics simpler.
type LRU struct {
	mu         sync.Mutex
	maxEntries int
	ttl        time.Duration
	clock      util.Clock
	ll         *list.List
	items      map[string]*list.Element
}

// NewLRU constructs an LRU with the given capacity and TTL.
//
// maxEntries must be > 0; a zero or negative value is corrected to 1
// so a misconfiguration doesn't accidentally disable the cache.
// clock=nil falls back to util.SystemClock so production callers can
// pass a literal nil.
func NewLRU(maxEntries int, ttl time.Duration, clock util.Clock) *LRU {
	if maxEntries <= 0 {
		maxEntries = 1
	}
	if clock == nil {
		clock = util.SystemClock{}
	}
	return &LRU{
		maxEntries: maxEntries,
		ttl:        ttl,
		clock:      clock,
		ll:         list.New(),
		items:      make(map[string]*list.Element, maxEntries),
	}
}

// Get returns (value, true) when the key is present AND the entry has
// not expired. An expired entry is evicted as a side effect so future
// reads short-circuit.
func (c *LRU) Get(key string) (CheckInResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return CheckInResult{}, false
	}
	e := el.Value.(*entry)
	if !c.clock.Now().Before(e.expiresAt) {
		// Expired — evict and report miss.
		c.removeElement(el)
		return CheckInResult{}, false
	}
	c.ll.MoveToFront(el)
	return e.value, true
}

// Set inserts or refreshes the entry at key. The TTL window is reset to
// (now + ttl). If the cache is full the least-recently-used entry is
// evicted to make room.
func (c *LRU) Set(key string, v CheckInResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock.Now()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*entry)
		e.value = v
		e.expiresAt = now.Add(c.ttl)
		c.ll.MoveToFront(el)
		return
	}
	e := &entry{key: key, value: v, expiresAt: now.Add(c.ttl)}
	el := c.ll.PushFront(e)
	c.items[key] = el

	if c.ll.Len() > c.maxEntries {
		c.removeOldest()
	}
}

// removeOldest evicts the LRU end of the list. Holds c.mu.
func (c *LRU) removeOldest() {
	el := c.ll.Back()
	if el == nil {
		return
	}
	c.removeElement(el)
}

// removeElement detaches the element from both the list and the map.
// Holds c.mu.
func (c *LRU) removeElement(el *list.Element) {
	c.ll.Remove(el)
	delete(c.items, el.Value.(*entry).key)
}
