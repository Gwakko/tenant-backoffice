package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// AuthUser is what authentication needs to know about a user. PasswordHash is
// filled only by AuthUserRepo.ByEmail.
type AuthUser struct {
	ID           user.ID
	Status       user.Status
	TokenVersion int64
	PasswordHash user.PasswordHash
}

// IsActive reports whether the user may authenticate.
func (u AuthUser) IsActive() bool { return u.Status == user.StatusActive }

// AuthUserRepo reads and changes the credentials of users.
//
// ByEmail and ByID run before any actor exists (login, token check) and take no
// transaction: they call SECURITY DEFINER lookup functions that return exactly
// one user. Everything else works inside the transaction of the acting user.
type AuthUserRepo interface {
	// ByEmail returns the user with the given normalized email, with the
	// password hash. found is false when there is no such user.
	ByEmail(ctx context.Context, email user.Email) (u AuthUser, found bool, err error)
	// ByID returns the user without the password hash.
	ByID(ctx context.Context, id user.ID) (u AuthUser, found bool, err error)
	// ByIDInTx is ByID inside an open transaction.
	ByIDInTx(ctx context.Context, tx Tx, id user.ID) (u AuthUser, found bool, err error)
	// PasswordHash returns the stored hash of the acting user; id must be the
	// actor of tx.
	PasswordHash(ctx context.Context, tx Tx, id user.ID) (user.PasswordHash, error)
	// SetOwnPassword stores a new hash for the acting user.
	SetOwnPassword(ctx context.Context, tx Tx, hash user.PasswordHash) error
	// BumpTokenVersion increments token_version of the user, which makes every
	// access token issued before it stale.
	BumpTokenVersion(ctx context.Context, tx Tx, id user.ID) error
}

// RefreshToken is the stored side of a refresh token: only the sha256 of the
// token, never the token itself.
type RefreshToken struct {
	ID        uuid.UUID
	UserID    user.ID
	FamilyID  uuid.UUID
	Hash      []byte
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

// RefreshTokenRepo stores refresh tokens. The table holds no tenant data and
// has no row-level security, so OwnerOf works without an actor; it only tells
// the use case whose transaction to open.
type RefreshTokenRepo interface {
	// OwnerOf returns the user the token hash belongs to.
	OwnerOf(ctx context.Context, hash []byte) (id user.ID, found bool, err error)
	// GetForUpdate reads the token and locks its row until the transaction ends,
	// so two concurrent uses of one token are serialised.
	GetForUpdate(ctx context.Context, tx Tx, hash []byte) (t RefreshToken, found bool, err error)
	Insert(ctx context.Context, tx Tx, t RefreshToken) error
	// MarkUsed sets used_at of one token.
	MarkUsed(ctx context.Context, tx Tx, id uuid.UUID, at time.Time) error
	// RevokeFamily sets revoked_at on every token of the family not yet revoked.
	RevokeFamily(ctx context.Context, tx Tx, family uuid.UUID, at time.Time) error
	// RevokeAllForUser revokes every family of the user.
	RevokeAllForUser(ctx context.Context, tx Tx, id user.ID, at time.Time) error
}

// ProfileRepo reads what the acting user may know about themselves.
type ProfileRepo interface {
	// OwnEmail returns the email of the acting user (the transaction runs as
	// that user; any other id is apperr.NotFound).
	OwnEmail(ctx context.Context, tx Tx, id user.ID) (user.Email, error)
}

// AccessToken is an issued access token.
type AccessToken struct {
	Token     string
	ExpiresAt time.Time
}

// AccessClaims are the verified claims of an access token.
type AccessClaims struct {
	Subject      user.ID
	TokenVersion int64
}

// TokenIssuer signs access tokens.
type TokenIssuer interface {
	Issue(sub user.ID, tokenVersion int64) (AccessToken, error)
}

// TokenVerifier checks an access token. It returns apperr.TokenExpired for an
// expired token and apperr.TokenInvalid for anything else that is wrong.
type TokenVerifier interface {
	Verify(token string) (AccessClaims, error)
}

// RefreshCodec creates refresh tokens and derives the stored hash.
type RefreshCodec interface {
	// Generate returns a new random token and its hash.
	Generate() (token string, hash []byte, err error)
	// Hash returns the hash of a presented token; a malformed token is
	// apperr.RefreshInvalid.
	Hash(token string) ([]byte, error)
}
