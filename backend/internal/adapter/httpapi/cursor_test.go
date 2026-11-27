package httpapi_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var (
	keyA = []byte("0123456789abcdef0123456789abcdef")
	keyB = []byte("fedcba9876543210fedcba9876543210")
)

func newCodec(t *testing.T, clk *testClock, keys ...[]byte) *httpapi.CursorCodec {
	t.Helper()
	c, err := httpapi.NewCursorCodec(keys, time.Hour, clk)
	require.NoError(t, err)
	return c
}

var binding = httpapi.CursorBinding{Scope: nodeA, Filters: map[string]string{"kind": "credit", "from": "2026-01-01"}}

func requireInvalidCursor(t *testing.T, err error, msg string) {
	t.Helper()
	require.ErrorIs(t, err, apperr.New(apperr.InvalidCursor, ""), msg)
}

// forge builds a cursor with a valid signature over an arbitrary payload.
func forge(key []byte, payload any) string {
	body, _ := json.Marshal(payload)
	head := base64.RawURLEncoding.EncodeToString(body)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(head))
	return head + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Test-ID: T-U-28
func TestCursorTampering(t *testing.T) {
	clk := newTestClock()
	codec := newCodec(t, clk, keyA)
	token, err := codec.Encode([]string{"2026-02-01T10:00:00Z", "0190f3a0-0000-7000-8000-000000000001"}, binding)
	require.NoError(t, err)

	got, err := codec.Decode(token, binding)
	require.NoError(t, err)
	require.Equal(t, []string{"2026-02-01T10:00:00Z", "0190f3a0-0000-7000-8000-000000000001"}, got)

	head, sig, _ := strings.Cut(token, ".")
	flip := func(s string) string { // change one character
		b := []byte(s)
		if b[len(b)/2] == 'A' {
			b[len(b)/2] = 'B'
		} else {
			b[len(b)/2] = 'A'
		}
		return string(b)
	}

	_, err = codec.Decode(flip(head)+"."+sig, binding)
	requireInvalidCursor(t, err, "a changed byte of the payload")
	_, err = codec.Decode(head+"."+flip(sig), binding)
	requireInvalidCursor(t, err, "a changed byte of the signature")
	_, err = codec.Decode(head+".", binding)
	requireInvalidCursor(t, err, "no signature")
	_, err = codec.Decode(head, binding)
	requireInvalidCursor(t, err, "no separator")
	_, err = codec.Decode(token+".x", binding)
	requireInvalidCursor(t, err, "extra part")
	_, err = codec.Decode("", binding)
	requireInvalidCursor(t, err, "empty")
	_, err = codec.Decode(strings.Repeat("a", 5000), binding)
	requireInvalidCursor(t, err, "too long")

	// A cursor signed with a key the server does not know.
	other := newCodec(t, clk, keyB)
	foreign, err := other.Encode([]string{"x"}, binding)
	require.NoError(t, err)
	_, err = codec.Decode(foreign, binding)
	requireInvalidCursor(t, err, "foreign signature")

	// Another version, even with a valid signature.
	_, err = codec.Decode(forge(keyA, map[string]any{"v": 2, "k": []string{"x"}, "f": "00", "e": clk.Now().Add(time.Hour).Unix()}), binding)
	requireInvalidCursor(t, err, "version 2")
	_, err = codec.Decode(forge(keyA, map[string]any{"v": 1, "k": []string{"x"}, "f": "00", "e": 1, "extra": true}), binding)
	requireInvalidCursor(t, err, "unknown payload field")

	// Other filters or another scope.
	otherFilters := httpapi.CursorBinding{Scope: nodeA, Filters: map[string]string{"kind": "debit", "from": "2026-01-01"}}
	_, err = codec.Decode(token, otherFilters)
	requireInvalidCursor(t, err, "other filters (EC-62)")
	otherScope := httpapi.CursorBinding{Scope: nodeB, Filters: binding.Filters}
	_, err = codec.Decode(token, otherScope)
	requireInvalidCursor(t, err, "other scope (EC-62)")
	extraFilter := httpapi.CursorBinding{Scope: nodeA, Filters: map[string]string{"kind": "credit", "from": "2026-01-01", "to": "2026-02-01"}}
	_, err = codec.Decode(token, extraFilter)
	requireInvalidCursor(t, err, "an added filter")
	_, err = codec.Decode(token, httpapi.CursorBinding{Scope: nodeA})
	requireInvalidCursor(t, err, "a dropped filter")

	// Filter order in the map does not matter, it is a map.
	same := httpapi.CursorBinding{Scope: nodeA, Filters: map[string]string{"from": "2026-01-01", "kind": "credit"}}
	_, err = codec.Decode(token, same)
	require.NoError(t, err)

	// Expiry.
	clk.Advance(time.Hour + time.Second)
	_, err = codec.Decode(token, binding)
	requireInvalidCursor(t, err, "expired")
}

func TestCursorRotationAcceptsOldKey(t *testing.T) {
	clk := newTestClock()
	old := newCodec(t, clk, keyA)
	token, err := old.Encode([]string{"x"}, binding)
	require.NoError(t, err)

	rotated := newCodec(t, clk, keyB, keyA) // new key signs, old key still verifies
	_, err = rotated.Decode(token, binding)
	require.NoError(t, err)
	fresh, err := rotated.Encode([]string{"y"}, binding)
	require.NoError(t, err)
	_, err = old.Decode(fresh, binding)
	requireInvalidCursor(t, err, "the old key does not know the new signature")

	dropped := newCodec(t, clk, keyB)
	_, err = dropped.Decode(token, binding)
	requireInvalidCursor(t, err, "after the old key is removed")
}

func TestNewCursorCodecValidation(t *testing.T) {
	clk := newTestClock()
	_, err := httpapi.NewCursorCodec(nil, time.Hour, clk)
	require.Error(t, err)
	_, err = httpapi.NewCursorCodec([][]byte{[]byte("short")}, time.Hour, clk)
	require.Error(t, err)
	_, err = httpapi.NewCursorCodec([][]byte{keyA}, 0, clk)
	require.Error(t, err)
	_, err = httpapi.NewCursorCodec([][]byte{keyA}, time.Hour, nil)
	require.Error(t, err)
}

func TestCursorIsOpaque(t *testing.T) {
	codec := newCodec(t, newTestClock(), keyA)
	token, err := codec.Encode([]string{"a"}, binding)
	require.NoError(t, err)
	require.Regexp(t, `^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`, token)
}

func TestParseLimit(t *testing.T) {
	v, err := httpapi.ParseLimit(nil)
	require.NoError(t, err)
	require.Equal(t, 50, v)
	for _, ok := range []int{1, 50, 200} {
		v, err := httpapi.ParseLimit(&ok)
		require.NoError(t, err)
		require.Equal(t, ok, v)
	}
	for _, bad := range []int{0, -1, 201} {
		_, err := httpapi.ParseLimit(&bad)
		require.ErrorIs(t, err, apperr.New(apperr.ValidationFailed, ""))
	}
}
