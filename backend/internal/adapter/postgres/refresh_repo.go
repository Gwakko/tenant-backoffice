package postgres

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

const resourceRefresh = "refresh token"

const (
	refreshOwner = `SELECT user_id FROM refresh_tokens WHERE token_hash = $1`

	refreshForUpdate = `SELECT id, user_id, family_id, token_hash, expires_at, used_at, revoked_at, created_at
  FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`

	refreshInsert = `INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6)`

	refreshMarkUsed     = `UPDATE refresh_tokens SET used_at = $2 WHERE id = $1`
	refreshRevokeFamily = `UPDATE refresh_tokens SET revoked_at = $2 WHERE family_id = $1 AND revoked_at IS NULL`
	refreshRevokeUser   = `UPDATE refresh_tokens SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`
)

// RefreshTokenRepo implements port.RefreshTokenRepo. The table has no row-level
// security (it holds no tenant data), so OwnerOf may use the pool without an
// actor; every state change happens in a transaction.
type RefreshTokenRepo struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

var _ port.RefreshTokenRepo = (*RefreshTokenRepo)(nil)

// NewRefreshTokenRepo builds the repository on the application pool.
func NewRefreshTokenRepo(pool *pgxpool.Pool, log *slog.Logger) *RefreshTokenRepo {
	return &RefreshTokenRepo{pool: pool, log: log}
}

// OwnerOf implements port.RefreshTokenRepo.
func (r *RefreshTokenRepo) OwnerOf(ctx context.Context, hash []byte) (user.ID, bool, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, refreshOwner, hash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return user.ID{}, false, nil
	}
	if err != nil {
		return user.ID{}, false, mapError(r.log, "refresh token owner", resourceRefresh, err)
	}
	return user.ID(id), true, nil
}

// GetForUpdate implements port.RefreshTokenRepo.
func (r *RefreshTokenRepo) GetForUpdate(ctx context.Context, tx port.Tx, hash []byte) (port.RefreshToken, bool, error) {
	t, err := unwrap(tx)
	if err != nil {
		return port.RefreshToken{}, false, err
	}
	var (
		rt      port.RefreshToken
		userID  uuid.UUID
		usedAt  *time.Time
		revoked *time.Time
	)
	err = t.QueryRow(ctx, refreshForUpdate, hash).Scan(
		&rt.ID, &userID, &rt.FamilyID, &rt.Hash, &rt.ExpiresAt, &usedAt, &revoked, &rt.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.RefreshToken{}, false, nil
	}
	if err != nil {
		return port.RefreshToken{}, false, mapError(r.log, "get refresh token", resourceRefresh, err)
	}
	rt.UserID, rt.UsedAt, rt.RevokedAt = user.ID(userID), usedAt, revoked
	return rt, true, nil
}

// Insert implements port.RefreshTokenRepo.
func (r *RefreshTokenRepo) Insert(ctx context.Context, tx port.Tx, rt port.RefreshToken) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	if _, err := t.Exec(ctx, refreshInsert, rt.ID, uuid.UUID(rt.UserID), rt.FamilyID, rt.Hash, rt.ExpiresAt, rt.CreatedAt); err != nil {
		return mapError(r.log, "insert refresh token", resourceRefresh, err)
	}
	return nil
}

// MarkUsed implements port.RefreshTokenRepo.
func (r *RefreshTokenRepo) MarkUsed(ctx context.Context, tx port.Tx, id uuid.UUID, at time.Time) error {
	return r.exec(ctx, tx, "mark refresh token used", refreshMarkUsed, id, at)
}

// RevokeFamily implements port.RefreshTokenRepo.
func (r *RefreshTokenRepo) RevokeFamily(ctx context.Context, tx port.Tx, family uuid.UUID, at time.Time) error {
	return r.exec(ctx, tx, "revoke refresh family", refreshRevokeFamily, family, at)
}

// RevokeAllForUser implements port.RefreshTokenRepo.
func (r *RefreshTokenRepo) RevokeAllForUser(ctx context.Context, tx port.Tx, id user.ID, at time.Time) error {
	return r.exec(ctx, tx, "revoke refresh tokens of user", refreshRevokeUser, uuid.UUID(id), at)
}

func (r *RefreshTokenRepo) exec(ctx context.Context, tx port.Tx, op, sql string, args ...any) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	if _, err := t.Exec(ctx, sql, args...); err != nil {
		return mapError(r.log, op, resourceRefresh, err)
	}
	return nil
}
