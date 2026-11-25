package auth

import (
	"context"
	"fmt"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// ChangePasswordInput is UC-A4. RequestID is reserved for the audit entry.
type ChangePasswordInput struct {
	Actor           user.ActorID
	CurrentPassword string
	NewPassword     string
	RequestID       string
}

// Validate checks the new password (12..128 bytes, valid UTF-8) and that it
// differs from the current one.
func (in ChangePasswordInput) Validate() error {
	if in.Actor.IsZero() {
		return apperr.New(apperr.ValidationFailed, "actor is required")
	}
	if in.CurrentPassword == "" || len(in.CurrentPassword) > maxPasswordBytes {
		return apperr.InvalidCredentialsError()
	}
	if err := user.ValidatePassword(in.NewPassword); err != nil {
		return err
	}
	if in.NewPassword == in.CurrentPassword {
		return apperr.New(apperr.InvalidPassword, "the new password must differ from the current one")
	}
	return nil
}

// ChangePasswordUseCase is UC-A4.
type ChangePasswordUseCase struct{ d Deps }

// NewChangePasswordUseCase builds the use case.
func NewChangePasswordUseCase(d Deps) (*ChangePasswordUseCase, error) {
	if err := d.need(d.Tx != nil && d.Users != nil && d.Refresh != nil && d.Hasher != nil && d.Clock != nil); err != nil {
		return nil, err
	}
	return &ChangePasswordUseCase{d: d}, nil
}

// Execute verifies the current password, stores the new hash, bumps
// token_version and revokes every refresh family of the user, all in one
// transaction: after a commit no old token of any kind works, and a failure
// leaves everything as it was.
//
// The audit entry auth.password_changed (spec 04, UC-A4) needs the audit store
// of a later milestone and is not written yet.
func (uc *ChangePasswordUseCase) Execute(ctx context.Context, in ChangePasswordInput) error {
	if err := in.Validate(); err != nil {
		return err
	}
	now := uc.d.Clock.Now()
	err := uc.d.Tx.InTx(ctx, in.Actor, port.TxOptions{}, func(ctx context.Context, tx port.Tx) error {
		current, err := uc.d.Users.PasswordHash(ctx, tx, in.Actor)
		if err != nil {
			return err
		}
		match, err := uc.d.Hasher.Verify(current, in.CurrentPassword)
		if err != nil {
			return fmt.Errorf("auth: verify password: %w", err)
		}
		if !match {
			return apperr.InvalidCredentialsError()
		}
		hash, err := uc.d.Hasher.Hash(in.NewPassword)
		if err != nil {
			return fmt.Errorf("auth: hash password: %w", err)
		}
		if err := uc.d.Users.SetOwnPassword(ctx, tx, hash); err != nil {
			return err
		}
		if err := uc.d.Users.BumpTokenVersion(ctx, tx, in.Actor); err != nil {
			return err
		}
		return uc.d.Refresh.RevokeAllForUser(ctx, tx, in.Actor, now)
	})
	if err != nil {
		return err
	}
	uc.d.logger().Info("password changed", "user_id", in.Actor.String(), "request_id", in.RequestID)
	return nil
}
