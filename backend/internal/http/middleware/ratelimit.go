package middleware

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lboyeon1223/gym-check-in/backend/internal/util"
)

// Limiter is an in-memory IP-keyed token bucket suitable only for a single
// backend instance — see CRITICAL note in backend/CLAUDE.md.
//
// Replenishment is continuous: each bucket holds up to `max` tokens and
// refills at rate (max / window). On each Allow call we credit
// (now - lastRefill) * rate tokens, cap at max, then deduct one token if
// at least one is available.
//
// Stale buckets (no traffic for `window`) are pruned lazily inside Allow.
// A scaling out beyond one process — or sustained > ~10 IPs/sec churn —
// requires migrating to Redis (ADR-008 trigger / ADR-016).
type Limiter struct {
	window time.Duration
	max    int
	clock  util.Clock

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
}

// NewLimiter constructs a Limiter with `max` tokens per `window`.
// `clock` is injectable so tests can advance time deterministically;
// production code should pass util.SystemClock{}.
func NewLimiter(window time.Duration, max int, clock util.Clock) *Limiter {
	if clock == nil {
		clock = util.SystemClock{}
	}
	return &Limiter{
		window:  window,
		max:     max,
		clock:   clock,
		buckets: make(map[string]*bucket),
	}
}

// Allow attempts to charge a single token from `ip`'s bucket. It returns
// true and `0` retry-after on success, or false and the suggested
// Retry-After (in seconds) when no tokens are left.
func (l *Limiter) Allow(ip string) (bool, int) {
	now := l.clock.Now()
	rate := float64(l.max) / l.window.Seconds() // tokens per second

	l.mu.Lock()
	defer l.mu.Unlock()

	// Lazy GC: drop buckets that haven't been touched for a full window.
	// Bound the scan so a sudden traffic spike doesn't pay quadratic cost.
	if len(l.buckets) > 0 && len(l.buckets) > l.max*4 {
		l.gcLocked(now)
	}

	b, ok := l.buckets[ip]
	if !ok {
		b = &bucket{tokens: float64(l.max), lastRefill: now}
		l.buckets[ip] = b
	} else {
		elapsed := now.Sub(b.lastRefill).Seconds()
		if elapsed > 0 {
			b.tokens = math.Min(float64(l.max), b.tokens+elapsed*rate)
			b.lastRefill = now
		}
	}
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens -= 1
		return true, 0
	}
	// Not enough tokens — recommend the caller retry after enough time
	// has elapsed for one full token to regenerate.
	retry := int(math.Ceil((1 - b.tokens) / rate))
	if retry < 1 {
		retry = 1
	}
	return false, retry
}

func (l *Limiter) gcLocked(now time.Time) {
	for ip, b := range l.buckets {
		if now.Sub(b.lastSeen) > l.window {
			delete(l.buckets, ip)
		}
	}
}

// Middleware returns a Gin handler that consults Allow on every request.
// On 429, the response carries Retry-After (seconds) and the standard
// apperr envelope with code RATE_LIMITED.
//
// Apply this only to routes that need rate limiting (e.g. /api/admin/login,
// /api/admin/refresh). Healthz and the broader API are NOT rate limited
// here — backend/CLAUDE.md keeps healthz exempt and ADR-013 documents the
// check-in endpoint's separate 5-second LRU.
func (l *Limiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ok, retry := l.Allow(c.ClientIP())
		if ok {
			c.Next()
			return
		}
		c.Set(ErrorCodeContextKey, "RATE_LIMITED")
		c.Writer.Header().Set("Retry-After", strconv.Itoa(retry))
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": gin.H{
				"code":    "RATE_LIMITED",
				"message": "too many requests; retry later",
			},
		})
	}
}
