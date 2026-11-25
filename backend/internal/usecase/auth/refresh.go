package auth

import (
	"context"
	"fmt"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// RefreshInput is the refresh token presented by the client.
type RefreshInput struct {
	RefreshToken string
}

// Validate checks the shape of the token: base64url, 43 characters.
func (in RefreshInput) Validate() error {
	if !validRefreshShape(in.RefreshToken) {
		return apperr.RefreshInvalidError()
	}
	return nil
}

func validRefreshShape(s string) bool {
	if len(s) != 43 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// RefreshOutput is the rotated session.
type RefreshOutput struct{ Session }

// RefreshUseCase is UC-A2.
type RefreshUseCase struct{ d Deps }

// NewRefreshUseCase builds the use case.
func NewRefreshUseCase(d Deps) (*RefreshUseCase, error) {
	if err := d.need(d.Tx != nil && d.Users != nil && d.Refresh != nil && d.Codec != nil &&
		d.Issuer != nil && d.Clock != nil && d.IDs != nil && d.Config.RefreshTTL > 0); err != nil {
		return nil, err
	}
	return &RefreshUseCase{d: d}, nil
}

// Execute rotates the token: the presented one is marked used and a new one of
// the same family is issued. A token that was already used is a replay: the
// whole family is revoked and the call fails. That revocation must be
// committed, so the transaction function reports the failure through a value
// and returns nil; returning an error would roll the revocation back.
func (uc *RefreshUseCase) Execute(ctx context.Context, in RefreshInput) (RefreshOutput, error) {
	log := uc.d.logger()
	if err := in.Validate(); err != nil {
		return RefreshOutput{}, err
	}
	hash, err := uc.d.Codec.Hash(in.RefreshToken)
	if err != nil {
		return RefreshOutput{}, err
	}
	owner, found, err := uc.d.Refresh.OwnerOf(ctx, hash)
	if err != nil {
		return RefreshOutput{}, err
	}
	if !found {
		return RefreshOutput{}, apperr.RefreshInvalidError()
	}

	now := uc.d.Clock.Now()
	var (
		out      RefreshOutput
		rejected bool
	)
	err = uc.d.Tx.InTx(ctx, owner, port.TxOptions{}, func(ctx context.Context, tx port.Tx) error {
		t, found, err := uc.d.Refresh.GetForUpdate(ctx, tx, hash)
		if err != nil {
			return err
		}
		if !found || t.RevokedAt != nil {
			rejected = true
			return nil
		}
		if t.UsedAt != nil {
			log.Warn("refresh token reused, family revoked", "user_id", owner.String(), "family_id", t.FamilyID.String())
			rejected = true
			return uc.d.Refresh.RevokeFamily(ctx, tx, t.FamilyID, now)
		}
		if !now.Before(t.ExpiresAt) {
			rejected = true
			return nil
		}
		u, found, err := uc.d.Users.ByIDInTx(ctx, tx, owner)
		if err != nil {
			return err
		}
		if !found || !u.IsActive() {
			rejected = true
			return uc.d.Refresh.RevokeFamily(ctx, tx, t.FamilyID, now)
		}
		if err := uc.d.Refresh.MarkUsed(ctx, tx, t.ID, now); err != nil {
			return err
		}
		token, row, err := newRefresh(uc.d, owner, t.FamilyID, now)
		if err != nil {
			return err
		}
		if err := uc.d.Refresh.Insert(ctx, tx, row); err != nil {
			return err
		}
		at, expiresIn, err := accessFor(uc.d, owner, u.TokenVersion, now)
		if err != nil {
			return err
		}
		out = RefreshOutput{Session{
			AccessToken: at.Token, ExpiresIn: expiresIn, RefreshToken: token,
			RefreshExpiresAt: row.ExpiresAt, UserID: owner,
		}}
		return nil
	})
	if err != nil {
		return RefreshOutput{}, fmt.Errorf("auth: refresh: %w", err)
	}
	if rejected {
		return RefreshOutput{}, apperr.RefreshInvalidError()
	}
	return out, nil
}
