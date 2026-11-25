package postgres

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

const (
	// tb_user_id_by_email is SECURITY DEFINER: a user of another branch is
	// invisible through the users policy. It answers only to an actor that
	// holds a role somewhere.
	userIDByEmail = `SELECT tb_user_id_by_email($1)`

	// No RETURNING and no ON CONFLICT: under row-level security a new user is
	// invisible to its creator until the first assignment exists, and both
	// clauses make PostgreSQL check the new row against the SELECT policy.
	insertUser = `INSERT INTO users (id, email, password_hash, status, token_version, created_at)
VALUES ($1, $2, $3, 'active', 0, $4)`

	constraintUserEmail = "users_email"

	selectUserStatus = `SELECT status FROM users WHERE id = $1`
)

// UserRepo implements port.UserRepo.
type UserRepo struct{ log *slog.Logger }

var _ port.UserRepo = (*UserRepo)(nil)

// NewUserRepo builds the repository.
func NewUserRepo(log *slog.Logger) *UserRepo { return &UserRepo{log: log} }

// IDByEmail implements port.UserRepo.
func (r *UserRepo) IDByEmail(ctx context.Context, tx port.Tx, email user.Email) (user.ID, bool, error) {
	t, err := unwrap(tx)
	if err != nil {
		return user.ID{}, false, err
	}
	var id *uuid.UUID
	if err := t.QueryRow(ctx, userIDByEmail, email.String()).Scan(&id); err != nil {
		return user.ID{}, false, mapError(r.log, "user id by email", resourceUser, err)
	}
	if id == nil {
		return user.ID{}, false, nil
	}
	return user.ID(*id), true, nil
}

// Insert implements port.UserRepo. A taken email is detected by the unique
// index, so the insert runs in a savepoint: the failed statement is undone
// without aborting the transaction of the use case. When another transaction
// is inserting the same email, the statement waits for it to finish.
func (r *UserRepo) Insert(ctx context.Context, tx port.Tx, u port.NewUser) (bool, error) {
	t, err := unwrap(tx)
	if err != nil {
		return false, err
	}
	sp, err := t.Begin(ctx)
	if err != nil {
		return false, mapError(r.log, "insert user", resourceUser, err)
	}
	_, err = sp.Exec(ctx, insertUser, uuid.UUID(u.ID), u.Email.String(), string(u.Hash), u.CreatedAt)
	if err != nil {
		if rbErr := sp.Rollback(ctx); rbErr != nil {
			r.log.Error("rollback of savepoint failed", "error", rbErr)
		}
		if pe, ok := pgError(err); ok && pe.Code == codeUniqueViolation && pe.ConstraintName == constraintUserEmail {
			return false, nil
		}
		return false, mapError(r.log, "insert user", resourceUser, err)
	}
	if err := sp.Commit(ctx); err != nil {
		return false, mapError(r.log, "insert user", resourceUser, err)
	}
	return true, nil
}

// Status implements port.UserRepo.
func (r *UserRepo) Status(ctx context.Context, tx port.Tx, id user.ID) (user.Status, error) {
	t, err := unwrap(tx)
	if err != nil {
		return "", err
	}
	var s string
	err = t.QueryRow(ctx, selectUserStatus, uuid.UUID(id)).Scan(&s)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apperr.NotFoundError(resourceUser)
	}
	if err != nil {
		return "", mapError(r.log, "user status", resourceUser, err)
	}
	return user.Status(s), nil
}
