package auth

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// LogoutInput is the refresh token of the session to end.
type LogoutInput struct {
	RefreshToken string
}

// Validate accepts anything: logout never fails on the token's shape.
func (in LogoutInput) Validate() error { return nil }

// LogoutUseCase is UC-A3.
type LogoutUseCase struct{ d Deps }

// NewLogoutUseCase builds the use case.
func NewLogoutUseCase(d Deps) (*LogoutUseCase, error) {
	if err := d.need(d.Tx != nil && d.Refresh != nil && d.Codec != nil && d.Clock != nil); err != nil {
		return nil, err
	}
	return &LogoutUseCase{d: d}, nil
}

// Execute revokes the family of the token. A malformed or unknown token is also
// a success: the client ends up logged out either way and learns nothing about
// which tokens exist.
func (uc *LogoutUseCase) Execute(ctx context.Context, in LogoutInput) error {
	if !validRefreshShape(in.RefreshToken) {
		return nil
	}
	hash, err := uc.d.Codec.Hash(in.RefreshToken)
	if err != nil {
		return nil
	}
	owner, found, err := uc.d.Refresh.OwnerOf(ctx, hash)
	if err != nil || !found {
		return err
	}
	now := uc.d.Clock.Now()
	return uc.d.Tx.InTx(ctx, owner, port.TxOptions{}, func(ctx context.Context, tx port.Tx) error {
		t, found, err := uc.d.Refresh.GetForUpdate(ctx, tx, hash)
		if err != nil || !found {
			return err
		}
		return uc.d.Refresh.RevokeFamily(ctx, tx, t.FamilyID, now)
	})
}
