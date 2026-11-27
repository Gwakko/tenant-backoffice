package auth

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// GetProfileInput is the request of GET /me for the user part.
type GetProfileInput struct{ Actor user.ActorID }

// Validate checks the input without touching the store.
func (in GetProfileInput) Validate() error {
	if in.Actor.IsZero() {
		return apperr.New(apperr.ValidationFailed, "actor is required").WithField("actor", "required")
	}
	return nil
}

// GetProfileOutput is the acting user.
type GetProfileOutput struct {
	ID    user.ID
	Email user.Email
}

// GetProfileUseCase reads the acting user's own record.
type GetProfileUseCase struct{ d Deps }

// NewGetProfileUseCase builds the use case.
func NewGetProfileUseCase(d Deps) (*GetProfileUseCase, error) {
	if err := d.need(d.Tx != nil && d.Profile != nil); err != nil {
		return nil, err
	}
	return &GetProfileUseCase{d: d}, nil
}

// Execute reads the profile in a read-only transaction of the actor.
func (uc *GetProfileUseCase) Execute(ctx context.Context, in GetProfileInput) (GetProfileOutput, error) {
	if err := in.Validate(); err != nil {
		return GetProfileOutput{}, err
	}
	var out GetProfileOutput
	err := uc.d.Tx.InTx(ctx, in.Actor, port.TxOptions{ReadOnly: true}, func(ctx context.Context, tx port.Tx) error {
		email, err := uc.d.Profile.OwnEmail(ctx, tx, in.Actor)
		if err != nil {
			return err
		}
		out = GetProfileOutput{ID: in.Actor, Email: email}
		return nil
	})
	return out, err
}
