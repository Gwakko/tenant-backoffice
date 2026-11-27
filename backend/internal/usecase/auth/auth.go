// Package auth holds the authentication use cases: login, refresh, logout,
// password change and the check of an access token.
//
// Before a request is authenticated there is no actor. The use cases look the
// user up through SECURITY DEFINER functions (no transaction, no actor) and
// open a transaction only as the user they have just identified: after the
// password was verified (login) or whose refresh token row was found.
package auth

import (
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Config holds the settings of the use cases.
type Config struct {
	// RefreshTTL is the lifetime of a refresh token.
	RefreshTTL time.Duration
}

// Deps are the collaborators of the use cases; each use case takes the ones it
// needs and fails to construct when one of those is missing.
type Deps struct {
	Config  Config
	Tx      port.TxRunner
	Users   port.AuthUserRepo
	Profile port.ProfileRepo
	Refresh port.RefreshTokenRepo
	Codec   port.RefreshCodec
	Issuer  port.TokenIssuer
	Verify  port.TokenVerifier
	Hasher  user.Hasher
	Clock   port.Clock
	IDs     port.IDGen
	Log     *slog.Logger
}

// Session is the result of a successful login or refresh.
type Session struct {
	AccessToken      string
	ExpiresIn        time.Duration
	RefreshToken     string
	RefreshExpiresAt time.Time
	UserID           user.ID
}

// maxPasswordBytes bounds the password of a login before any hashing.
const maxPasswordBytes = user.MaxPasswordBytes

var errMissingDep = errors.New("auth: missing dependency")

func (d Deps) need(ok bool) error {
	if !ok {
		return errMissingDep
	}
	return nil
}

func (d Deps) logger() *slog.Logger {
	if d.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return d.Log
}

// newRefresh builds the stored row of a new refresh token in family and returns
// it together with the token text for the client.
func newRefresh(d Deps, uid user.ID, family uuid.UUID, now time.Time) (string, port.RefreshToken, error) {
	token, hash, err := d.Codec.Generate()
	if err != nil {
		return "", port.RefreshToken{}, err
	}
	return token, port.RefreshToken{
		ID: d.IDs.New(), UserID: uid, FamilyID: family, Hash: hash,
		ExpiresAt: now.Add(d.Config.RefreshTTL), CreatedAt: now,
	}, nil
}

// accessFor issues an access token and fills the access part of a session.
func accessFor(d Deps, uid user.ID, tokenVersion int64, now time.Time) (port.AccessToken, time.Duration, error) {
	at, err := d.Issuer.Issue(uid, tokenVersion)
	if err != nil {
		return port.AccessToken{}, 0, err
	}
	return at, at.ExpiresAt.Sub(now), nil
}
