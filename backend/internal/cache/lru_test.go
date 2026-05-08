package cache_test

import (
	"testing"
	"time"

	"github.com/lboyeon1223/gym-check-in/backend/internal/cache"
	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

func TestLRU_SetGet_HitsBeforeTTL(t *testing.T) {
	clock := &util.FakeClock{Instant: time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)}
	c := cache.NewLRU(8, 5*time.Second, clock)

	want := cache.CheckInResult{ID: 42, CheckedInAt: clock.Instant, Body: []byte(`{"ok":true}`), Status: 200}
	c.Set("1:2", want)

	// Same instant → hit.
	if got, ok := c.Get("1:2"); !ok || got.ID != 42 || string(got.Body) != `{"ok":true}` {
		t.Fatalf("Get at insertion time: got=%+v ok=%v", got, ok)
	}

	// Just before TTL → still hit.
	clock.Instant = clock.Instant.Add(4*time.Second + 999*time.Millisecond)
	if _, ok := c.Get("1:2"); !ok {
		t.Fatalf("Get at TTL-1ms should hit")
	}
}

func TestLRU_TTLExpiry_EvictsAndMisses(t *testing.T) {
	clock := &util.FakeClock{Instant: time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)}
	c := cache.NewLRU(8, 5*time.Second, clock)
	c.Set("a:b", cache.CheckInResult{ID: 1, Status: 200})

	// At exactly TTL the entry is expired (we use !Before == not strictly less).
	clock.Instant = clock.Instant.Add(5 * time.Second)
	if _, ok := c.Get("a:b"); ok {
		t.Fatalf("Get at exactly ttl should miss")
	}

	// After eviction the next Set must succeed without bumping size.
	c.Set("a:b", cache.CheckInResult{ID: 2, Status: 200})
	if got, ok := c.Get("a:b"); !ok || got.ID != 2 {
		t.Fatalf("Set after expiry: got=%+v ok=%v", got, ok)
	}
}

func TestLRU_MaxEvictsLeastRecentlyUsed(t *testing.T) {
	clock := &util.FakeClock{Instant: time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)}
	c := cache.NewLRU(2, 1*time.Hour, clock)

	c.Set("k1", cache.CheckInResult{ID: 1, Status: 200})
	c.Set("k2", cache.CheckInResult{ID: 2, Status: 200})
	// Touch k1 to make k2 the LRU end.
	if _, ok := c.Get("k1"); !ok {
		t.Fatalf("k1 should hit before eviction")
	}
	c.Set("k3", cache.CheckInResult{ID: 3, Status: 200})

	if _, ok := c.Get("k2"); ok {
		t.Fatalf("k2 should have been evicted as the LRU end")
	}
	if got, ok := c.Get("k1"); !ok || got.ID != 1 {
		t.Fatalf("k1 should still be present: got=%+v ok=%v", got, ok)
	}
	if got, ok := c.Get("k3"); !ok || got.ID != 3 {
		t.Fatalf("k3 should be present: got=%+v ok=%v", got, ok)
	}
}

func TestLRU_SetUpdatesValueAndRefreshesTTL(t *testing.T) {
	clock := &util.FakeClock{Instant: time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)}
	c := cache.NewLRU(4, 5*time.Second, clock)
	c.Set("k", cache.CheckInResult{ID: 1, Status: 200})
	clock.Instant = clock.Instant.Add(3 * time.Second)
	c.Set("k", cache.CheckInResult{ID: 2, Status: 200}) // bumps TTL window

	clock.Instant = clock.Instant.Add(4 * time.Second) // 7s past first Set, 4s past second
	if got, ok := c.Get("k"); !ok || got.ID != 2 {
		t.Fatalf("expected refreshed TTL hit (id=2), got=%+v ok=%v", got, ok)
	}
}

func TestLRU_NilClockFallsBackToSystem(t *testing.T) {
	// Sanity: a nil clock argument shouldn't panic — production passes a
	// concrete util.SystemClock{} but the constructor should accept nil too.
	c := cache.NewLRU(2, 5*time.Second, nil)
	c.Set("k", cache.CheckInResult{ID: 1, Status: 200})
	if _, ok := c.Get("k"); !ok {
		t.Fatalf("Get with system clock should hit")
	}
}
