package auth

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// AuthenticateInput is the access token of a request.
type AuthenticateInput struct {
	AccessToken string
}

// Validate rejects an empty token.
func (in AuthenticateInput) Validate() error {
	if in.AccessToken == "" {
		return apperr.TokenInvalidError()
	}
	return nil
}

// AuthenticateOutput is the authenticated actor.
type AuthenticateOutput struct {
	Actor user.ActorID
}

// AuthenticateUseCase is UC-A5, used by the HTTP middleware on every request.
type AuthenticateUseCase struct{ d Deps }

// NewAuthenticateUseCase builds the use case.
func NewAuthenticateUseCase(d Deps) (*AuthenticateUseCase, error) {
	if err := d.need(d.Users != nil && d.Verify != nil); err != nil {
		return nil, err
	}
	return &AuthenticateUseCase{d: d}, nil
}

// Execute verifies signature and claims, then checks the user in the database:
// it must exist, be active and carry the token_version of the token. Anything
// else is token_invalid (EC-105), an expired token token_expired.
func (uc *AuthenticateUseCase) Execute(ctx context.Context, in AuthenticateInput) (AuthenticateOutput, error) {
	if err := in.Validate(); err != nil {
		return AuthenticateOutput{}, err
	}
	claims, err := uc.d.Verify.Verify(in.AccessToken)
	if err != nil {
		return AuthenticateOutput{}, err
	}
	u, found, err := uc.d.Users.ByID(ctx, claims.Subject)
	if err != nil {
		return AuthenticateOutput{}, err
	}
	if !found || !u.IsActive() || u.TokenVersion != claims.TokenVersion {
		return AuthenticateOutput{}, apperr.TokenInvalidError()
	}
	return AuthenticateOutput{Actor: u.ID}, nil
}
