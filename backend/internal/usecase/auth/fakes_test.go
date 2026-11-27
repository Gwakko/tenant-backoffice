package auth_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

type fakeTx struct{}

func (fakeTx) TxMarker() {}

type userRec struct {
	id     user.ID
	email  user.Email
	hash   user.PasswordHash
	status user.Status
	tv     int64
}

// store is an in-memory database that also plays the transaction runner: a
// failed transaction restores the snapshot, like a rollback.
type store struct {
	mu     sync.Mutex
	users  map[user.ID]*userRec
	tokens []*port.RefreshToken
	actors []user.ActorID
	// failOn makes a repository call fail once the named operation runs.
	failOn string
}

func newStore() *store { return &store{users: map[user.ID]*userRec{}} }

func (s *store) addUser(email, password string, status user.Status) *userRec {
	u := &userRec{id: user.ID(uuid.New()), email: user.Email(email), hash: user.PasswordHash("h:" + password), status: status}
	s.users[u.id] = u
	return u
}

func (s *store) snapshot() (map[user.ID]userRec, []port.RefreshToken) {
	us := map[user.ID]userRec{}
	for k, v := range s.users {
		us[k] = *v
	}
	ts := make([]port.RefreshToken, len(s.tokens))
	for i, t := range s.tokens {
		ts[i] = *t
	}
	return us, ts
}

func (s *store) restore(us map[user.ID]userRec, ts []port.RefreshToken) {
	for k, v := range us {
		*s.users[k] = v // in place: tests hold pointers to the records
	}
	s.tokens = nil
	for _, t := range ts {
		t := t
		s.tokens = append(s.tokens, &t)
	}
}

func (s *store) InTx(ctx context.Context, actor user.ActorID, _ port.TxOptions, fn func(context.Context, port.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actors = append(s.actors, actor)
	us, ts := s.snapshot()
	if err := fn(ctx, fakeTx{}); err != nil {
		s.restore(us, ts)
		return err
	}
	return nil
}

func (s *store) fail(op string) error {
	if s.failOn == op {
		return errors.New("injected " + op)
	}
	return nil
}

// AuthUserRepo. The lookups run outside a transaction and so take no lock here.
func (s *store) ByEmail(_ context.Context, email user.Email) (port.AuthUser, bool, error) {
	for _, u := range s.users {
		if u.email == email {
			return port.AuthUser{ID: u.id, Status: u.status, TokenVersion: u.tv, PasswordHash: u.hash}, true, nil
		}
	}
	return port.AuthUser{}, false, nil
}

func (s *store) ByID(_ context.Context, id user.ID) (port.AuthUser, bool, error) {
	return s.byID(id)
}

func (s *store) ByIDInTx(_ context.Context, _ port.Tx, id user.ID) (port.AuthUser, bool, error) {
	return s.byID(id)
}

func (s *store) byID(id user.ID) (port.AuthUser, bool, error) {
	u, ok := s.users[id]
	if !ok {
		return port.AuthUser{}, false, nil
	}
	return port.AuthUser{ID: u.id, Status: u.status, TokenVersion: u.tv}, true, nil
}

func (s *store) OwnEmail(_ context.Context, _ port.Tx, id user.ID) (user.Email, error) {
	u, ok := s.users[id]
	if !ok {
		return "", apperr.NotFoundError("user")
	}
	return u.email, nil
}

func (s *store) PasswordHash(_ context.Context, _ port.Tx, id user.ID) (user.PasswordHash, error) {
	return s.users[id].hash, nil
}

func (s *store) SetOwnPassword(_ context.Context, _ port.Tx, hash user.PasswordHash) error {
	if err := s.fail("set-password"); err != nil {
		return err
	}
	s.users[s.actors[len(s.actors)-1]].hash = hash
	return nil
}

func (s *store) BumpTokenVersion(_ context.Context, _ port.Tx, id user.ID) error {
	if err := s.fail("bump"); err != nil {
		return err
	}
	s.users[id].tv++
	return nil
}

// RefreshTokenRepo.
func (s *store) OwnerOf(_ context.Context, hash []byte) (user.ID, bool, error) {
	for _, t := range s.tokens {
		if string(t.Hash) == string(hash) {
			return t.UserID, true, nil
		}
	}
	return user.ID{}, false, nil
}

func (s *store) GetForUpdate(_ context.Context, _ port.Tx, hash []byte) (port.RefreshToken, bool, error) {
	for _, t := range s.tokens {
		if string(t.Hash) == string(hash) {
			return *t, true, nil
		}
	}
	return port.RefreshToken{}, false, nil
}

func (s *store) Insert(_ context.Context, _ port.Tx, t port.RefreshToken) error {
	if err := s.fail("insert"); err != nil {
		return err
	}
	s.tokens = append(s.tokens, &t)
	return nil
}

func (s *store) MarkUsed(_ context.Context, _ port.Tx, id uuid.UUID, at time.Time) error {
	for _, t := range s.tokens {
		if t.ID == id {
			t.UsedAt = &at
		}
	}
	return nil
}

func (s *store) RevokeFamily(_ context.Context, _ port.Tx, family uuid.UUID, at time.Time) error {
	for _, t := range s.tokens {
		if t.FamilyID == family && t.RevokedAt == nil {
			t.RevokedAt = &at
		}
	}
	return nil
}

func (s *store) RevokeAllForUser(_ context.Context, _ port.Tx, id user.ID, at time.Time) error {
	if err := s.fail("revoke-all"); err != nil {
		return err
	}
	for _, t := range s.tokens {
		if t.UserID == id && t.RevokedAt == nil {
			t.RevokedAt = &at
		}
	}
	return nil
}

// fakeHasher "hashes" to "h:"+plain and counts Verify calls.
type fakeHasher struct {
	verifies []user.PasswordHash
	hashes   int
}

func (h *fakeHasher) Hash(plain string) (user.PasswordHash, error) {
	h.hashes++
	return user.PasswordHash("h:" + plain), nil
}

func (h *fakeHasher) Verify(hash user.PasswordHash, plain string) (bool, error) {
	h.verifies = append(h.verifies, hash)
	return string(hash) == "h:"+plain, nil
}

type fakeCodec struct{ n int }

func (c *fakeCodec) Generate() (string, []byte, error) {
	c.n++
	tok := fmt.Sprintf("%043d", c.n)
	return tok, []byte(tok), nil
}

func (c *fakeCodec) Hash(token string) ([]byte, error) {
	if len(token) != 43 {
		return nil, apperr.RefreshInvalidError()
	}
	return []byte(token), nil
}

type fakeIssuer struct{ clock *fakeClock }

func (i fakeIssuer) Issue(sub user.ID, tv int64) (port.AccessToken, error) {
	return port.AccessToken{Token: fmt.Sprintf("at:%s:%d", sub, tv), ExpiresAt: i.clock.Now().Add(10 * time.Minute)}, nil
}

func (i fakeIssuer) Verify(token string) (port.AccessClaims, error) {
	parts := strings.Split(token, ":")
	if len(parts) != 3 || parts[0] != "at" {
		return port.AccessClaims{}, apperr.TokenInvalidError()
	}
	id, err := user.ParseID(parts[1])
	if err != nil {
		return port.AccessClaims{}, apperr.TokenInvalidError()
	}
	var tv int64
	_, _ = fmt.Sscanf(parts[2], "%d", &tv)
	return port.AccessClaims{Subject: id, TokenVersion: tv}, nil
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

type fakeIDs struct{}

func (fakeIDs) New() uuid.UUID { return uuid.New() }

type world struct {
	t      *testing.T
	store  *store
	hasher *fakeHasher
	clock  *fakeClock
	deps   auth.Deps
}

func newWorld(t *testing.T) *world {
	t.Helper()
	st := newStore()
	clock := &fakeClock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	h := &fakeHasher{}
	iss := fakeIssuer{clock}
	return &world{t: t, store: st, hasher: h, clock: clock, deps: auth.Deps{
		Config: auth.Config{RefreshTTL: 7 * 24 * time.Hour},
		Tx:     st, Users: st, Profile: st, Refresh: st, Codec: &fakeCodec{}, Issuer: iss, Verify: iss,
		Hasher: h, Clock: clock, IDs: fakeIDs{},
	}}
}

func requireCode(t *testing.T, err error, want apperr.Code) {
	t.Helper()
	require.Error(t, err)
	code, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, want, code)
}

const goodPassword = "correct horse battery"
