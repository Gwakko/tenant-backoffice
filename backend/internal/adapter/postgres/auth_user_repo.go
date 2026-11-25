package postgres

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

const resourceUser = "user"

const (
	// The lookups are SECURITY DEFINER functions: they work without an actor
	// and return exactly one user. Only the by-email variant returns the hash.
	authByEmail = `SELECT id, password_hash, status, token_version FROM tb_auth_user_by_email($1)`
	authByID    = `SELECT id, status, token_version FROM tb_auth_user_by_id($1)`

	// The actor's own hash: the row is joined through the same function login
	// uses, and restricted to the actor, so no other user's hash can be read.
	ownHash = `SELECT a.password_hash
  FROM users u CROSS JOIN LATERAL tb_auth_user_by_email(u.email) a
 WHERE u.id = $1 AND u.id = tb_actor_id()`

	callSetOwnHash   = `SELECT tb_set_own_password($1)`
	incrementVersion = `UPDATE users SET token_version = token_version + 1 WHERE id = $1`
)

// AuthUserRepo implements port.AuthUserRepo. Lookups before authentication use
// the pool directly (no actor exists yet); the rest works in the transaction of
// the acting user.
type AuthUserRepo struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

var _ port.AuthUserRepo = (*AuthUserRepo)(nil)

// NewAuthUserRepo builds the repository on the application pool.
func NewAuthUserRepo(pool *pgxpool.Pool, log *slog.Logger) *AuthUserRepo {
	return &AuthUserRepo{pool: pool, log: log}
}

type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ByEmail implements port.AuthUserRepo.
func (r *AuthUserRepo) ByEmail(ctx context.Context, email user.Email) (port.AuthUser, bool, error) {
	var (
		id     uuid.UUID
		hash   string
		status string
		tv     int64
	)
	err := r.pool.QueryRow(ctx, authByEmail, email.String()).Scan(&id, &hash, &status, &tv)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.AuthUser{}, false, nil
	}
	if err != nil {
		return port.AuthUser{}, false, mapError(r.log, "auth user by email", resourceUser, err)
	}
	return port.AuthUser{ID: user.ID(id), Status: user.Status(status), TokenVersion: tv, PasswordHash: user.PasswordHash(hash)}, true, nil
}

// ByID implements port.AuthUserRepo.
func (r *AuthUserRepo) ByID(ctx context.Context, id user.ID) (port.AuthUser, bool, error) {
	return r.byID(ctx, r.pool, id)
}

// ByIDInTx implements port.AuthUserRepo.
func (r *AuthUserRepo) ByIDInTx(ctx context.Context, tx port.Tx, id user.ID) (port.AuthUser, bool, error) {
	t, err := unwrap(tx)
	if err != nil {
		return port.AuthUser{}, false, err
	}
	return r.byID(ctx, t, id)
}

func (r *AuthUserRepo) byID(ctx context.Context, q queryer, id user.ID) (port.AuthUser, bool, error) {
	var (
		got    uuid.UUID
		status string
		tv     int64
	)
	err := q.QueryRow(ctx, authByID, uuid.UUID(id)).Scan(&got, &status, &tv)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.AuthUser{}, false, nil
	}
	if err != nil {
		return port.AuthUser{}, false, mapError(r.log, "auth user by id", resourceUser, err)
	}
	return port.AuthUser{ID: user.ID(got), Status: user.Status(status), TokenVersion: tv}, true, nil
}

// PasswordHash implements port.AuthUserRepo.
func (r *AuthUserRepo) PasswordHash(ctx context.Context, tx port.Tx, id user.ID) (user.PasswordHash, error) {
	t, err := unwrap(tx)
	if err != nil {
		return "", err
	}
	var hash string
	err = t.QueryRow(ctx, ownHash, uuid.UUID(id)).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apperr.NotFoundError(resourceUser)
	}
	if err != nil {
		return "", mapError(r.log, "read own password hash", resourceUser, err)
	}
	return user.PasswordHash(hash), nil
}

// SetOwnPassword implements port.AuthUserRepo. The write goes through
// tb_set_own_password: tb_app has no UPDATE on password_hash.
func (r *AuthUserRepo) SetOwnPassword(ctx context.Context, tx port.Tx, hash user.PasswordHash) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	if _, err := t.Exec(ctx, callSetOwnHash, string(hash)); err != nil {
		return mapError(r.log, "set own password", resourceUser, err)
	}
	return nil
}

// BumpTokenVersion implements port.AuthUserRepo.
func (r *AuthUserRepo) BumpTokenVersion(ctx context.Context, tx port.Tx, id user.ID) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	tag, err := t.Exec(ctx, incrementVersion, uuid.UUID(id))
	if err != nil {
		return mapError(r.log, "bump token version", resourceUser, err)
	}
	if tag.RowsAffected() != 1 {
		return apperr.NotFoundError(resourceUser)
	}
	return nil
}
