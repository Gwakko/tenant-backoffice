// Package ratelimit is an in-memory token-bucket rate limiter. State lives in
// the memory of one process (spec 02 section 7: one API instance); idle keys
// are evicted and the number of keys is bounded.
package ratelimit

import (
	"math"
	"sync"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Limiter decides whether the request identified by key may proceed under a
// budget of limit requests per per (burst limit). When it may not, retryAfter
// says how long until the next request would be allowed.
type Limiter interface {
	Allow(key string, limit int, per time.Duration) (allowed bool, retryAfter time.Duration)
}

type bucket struct {
	tokens float64
	last   time.Time
	// idleAfter is the idle time after which the bucket is full again and
	// can be forgotten without changing any decision.
	idleAfter time.Duration
}

// Memory is a Limiter that keeps buckets in a map guarded by one mutex.
type Memory struct {
	clock   port.Clock
	maxKeys int

	mu        sync.Mutex
	buckets   map[string]*bucket
	nextSweep time.Time
}

const sweepEvery = time.Minute

// NewMemory builds a limiter. maxKeys bounds the state: when it is reached and
// no idle key can be dropped, a request with a new key is refused (fail closed)
// instead of growing the map.
func NewMemory(clock port.Clock, maxKeys int) *Memory {
	if maxKeys < 1 {
		maxKeys = 1
	}
	return &Memory{clock: clock, maxKeys: maxKeys, buckets: make(map[string]*bucket)}
}

// Allow implements Limiter.
func (m *Memory) Allow(key string, limit int, per time.Duration) (bool, time.Duration) {
	if limit < 1 || per <= 0 {
		return false, time.Second
	}
	now := m.clock.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	if !now.Before(m.nextSweep) {
		m.sweep(now)
	}
	b, ok := m.buckets[key]
	if !ok {
		if len(m.buckets) >= m.maxKeys {
			m.sweep(now)
		}
		if len(m.buckets) >= m.maxKeys {
			return false, per
		}
		b = &bucket{tokens: float64(limit), last: now, idleAfter: per}
		m.buckets[key] = b
	}

	lim := float64(limit)
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = math.Min(lim, b.tokens+lim*float64(elapsed)/float64(per))
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / lim * float64(per))
}

// Len returns the number of tracked keys (for tests and metrics).
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.buckets)
}

// sweep drops buckets that have been idle long enough to be full again.
// The caller holds mu.
func (m *Memory) sweep(now time.Time) {
	for k, b := range m.buckets {
		if now.Sub(b.last) >= b.idleAfter {
			delete(m.buckets, k)
		}
	}
	m.nextSweep = now.Add(sweepEvery)
}
