package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Pagination limits (spec 03 section 3).
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

const (
	cursorVersion = 1
	// maxCursorLen bounds the text that is decoded; a longer one is rejected
	// before any work is done.
	maxCursorLen = 2048
	// minCursorKeyLen is the shortest HMAC key the codec accepts.
	minCursorKeyLen = 32
)

// ParseLimit applies the default and checks the range of the limit parameter.
func ParseLimit(limit *int) (int, error) {
	if limit == nil {
		return DefaultLimit, nil
	}
	if *limit < 1 || *limit > MaxLimit {
		return 0, apperr.New(apperr.ValidationFailed, "limit is out of range").WithField("limit", "out_of_range")
	}
	return *limit, nil
}

// CursorBinding is what a cursor is valid for: the scope of the listing (for
// example the scope node id) and its filters. A cursor decodes only against the
// binding it was made for, so it cannot widen a listing (EC-62, EC-93).
type CursorBinding struct {
	Scope   string
	Filters map[string]string
}

func (b CursorBinding) hash() string {
	// json.Marshal sorts map keys, so the encoding is canonical.
	canon, _ := json.Marshal(struct {
		S string            `json:"s"`
		F map[string]string `json:"f"`
	}{b.Scope, b.Filters})
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}

type cursorPayload struct {
	V int      `json:"v"`
	K []string `json:"k"`
	F string   `json:"f"`
	E int64    `json:"e"`
}

// CursorCodec makes and checks opaque, signed pagination cursors:
// base64url(JSON{v,k,f,e}) + "." + base64url(HMAC-SHA256 of the first part).
// k is the sort key of the last item, f the hash of the binding, e the expiry
// (Unix seconds). The signature does not grant access (every request is
// authorized again); it makes client mistakes explicit.
type CursorCodec struct {
	keys  [][]byte
	ttl   time.Duration
	clock port.Clock
}

// NewCursorCodec builds a codec. keys[0] signs; all keys verify, which allows
// rotation: add the new key first, drop the old one after the cursor TTL.
func NewCursorCodec(keys [][]byte, ttl time.Duration, clock port.Clock) (*CursorCodec, error) {
	if len(keys) == 0 {
		return nil, errors.New("httpapi: at least one cursor key is required")
	}
	for _, k := range keys {
		if len(k) < minCursorKeyLen {
			return nil, fmt.Errorf("httpapi: cursor key must be at least %d bytes", minCursorKeyLen)
		}
	}
	if ttl <= 0 || clock == nil {
		return nil, errors.New("httpapi: cursor ttl must be positive and a clock is required")
	}
	return &CursorCodec{keys: keys, ttl: ttl, clock: clock}, nil
}

func sign(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// Encode makes the cursor that continues after the item with sort key k.
func (c *CursorCodec) Encode(k []string, b CursorBinding) (string, error) {
	body, err := json.Marshal(cursorPayload{
		V: cursorVersion, K: k, F: b.hash(), E: c.clock.Now().Add(c.ttl).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	head := base64.RawURLEncoding.EncodeToString(body)
	return head + "." + base64.RawURLEncoding.EncodeToString(sign(c.keys[0], head)), nil
}

// Decode checks a cursor against the binding of the current request and returns
// the sort key. Every failure is apperr.InvalidCursor with the same message.
func (c *CursorCodec) Decode(token string, b CursorBinding) ([]string, error) {
	bad := apperr.InvalidCursorError()
	if token == "" || len(token) > maxCursorLen {
		return nil, bad
	}
	head, sigText, ok := strings.Cut(token, ".")
	if !ok || strings.Contains(sigText, ".") {
		return nil, bad
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigText)
	if err != nil {
		return nil, bad
	}
	// Every key is tried without stopping early; hmac.Equal is constant time.
	valid := false
	for _, key := range c.keys {
		if hmac.Equal(sig, sign(key, head)) {
			valid = true
		}
	}
	if !valid {
		return nil, bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(head)
	if err != nil {
		return nil, bad
	}
	var p cursorPayload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil || dec.More() {
		return nil, bad
	}
	if p.V != cursorVersion {
		return nil, bad
	}
	if subtle.ConstantTimeCompare([]byte(p.F), []byte(b.hash())) != 1 {
		return nil, bad
	}
	if p.E <= c.clock.Now().Unix() {
		return nil, bad
	}
	return p.K, nil
}
