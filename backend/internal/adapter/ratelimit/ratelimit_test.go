package ratelimit_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/ratelimit"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// allow spends one request of a 5-per-minute budget.
func allow(l *ratelimit.Memory, key string) (bool, time.Duration) {
	return l.Allow(key, 5, time.Minute)
}

func ok(l *ratelimit.Memory, key string) bool {
	a, _ := allow(l, key)
	return a
}

func TestBurstThenRefusal(t *testing.T) {
	clk := newClock()
	l := ratelimit.NewMemory(clk, 100)
	for i := 0; i < 5; i++ {
		require.True(t, ok(l, "k"), "request %d", i+1)
	}
	allowed, retry := allow(l, "k")
	require.False(t, allowed)
	require.Equal(t, 12*time.Second, retry)
}

func TestRefillByClock(t *testing.T) {
	clk := newClock()
	l := ratelimit.NewMemory(clk, 100)
	for i := 0; i < 5; i++ {
		allow(l, "k")
	}
	require.False(t, ok(l, "k"))
	clk.Advance(11 * time.Second)
	require.False(t, ok(l, "k"), "less than one token refilled")
	clk.Advance(2 * time.Second)
	require.True(t, ok(l, "k"), "one token refilled after 12s")
	require.False(t, ok(l, "k"))
	clk.Advance(time.Hour)
	for i := 0; i < 5; i++ {
		require.True(t, ok(l, "k"))
	}
	require.False(t, ok(l, "k"), "refill never exceeds the burst")
}

func TestKeysAreIndependent(t *testing.T) {
	l := ratelimit.NewMemory(newClock(), 100)
	for i := 0; i < 5; i++ {
		allow(l, "a")
	}
	require.False(t, ok(l, "a"))
	require.True(t, ok(l, "b"))
}

func TestClockMovingBackwardsDoesNotRefill(t *testing.T) {
	clk := newClock()
	l := ratelimit.NewMemory(clk, 100)
	for i := 0; i < 5; i++ {
		allow(l, "k")
	}
	clk.Advance(-time.Hour)
	require.False(t, ok(l, "k"))
}

func TestInvalidRuleRefuses(t *testing.T) {
	l := ratelimit.NewMemory(newClock(), 10)
	allowed, _ := l.Allow("k", 0, time.Minute)
	require.False(t, allowed)
	allowed, _ = l.Allow("k", 5, 0)
	require.False(t, allowed)
}

func TestIdleKeysAreEvicted(t *testing.T) {
	clk := newClock()
	l := ratelimit.NewMemory(clk, 100)
	for i := 0; i < 50; i++ {
		allow(l, string(rune('a'+i%26))+string(rune('A'+i/26)))
	}
	require.Equal(t, 50, l.Len())
	clk.Advance(2 * time.Minute)
	allow(l, "fresh")
	require.Equal(t, 1, l.Len(), "idle keys are dropped on the next sweep")
}

func TestKeyCountIsBounded(t *testing.T) {
	clk := newClock()
	l := ratelimit.NewMemory(clk, 3)
	for _, k := range []string{"a", "b", "c"} {
		require.True(t, ok(l, k))
	}
	require.False(t, ok(l, "d"), "a new key is refused while the state is full of active keys")
	require.Equal(t, 3, l.Len())
	require.True(t, ok(l, "a"), "known keys keep working")
	clk.Advance(2 * time.Minute)
	require.True(t, ok(l, "d"), "idle keys make room")
}

func TestConcurrentAllowIsExact(t *testing.T) {
	l := ratelimit.NewMemory(newClock(), 100)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok(l, "k") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 5, allowed.Load())
}
