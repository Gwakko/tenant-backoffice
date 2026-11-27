package httpapi

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
)

// Login signs in with email and password. Every failure is the same
// invalid_credentials answer, so the response says nothing about whether the
// email exists. The refresh token goes only into the cookie.
func (h *CoreHandlers) Login(ctx context.Context, req gen.LoginRequestObject) (gen.LoginResponseObject, error) {
	if req.Body == nil {
		return nil, apperr.InvalidCredentialsError()
	}
	ci := ClientFrom(ctx)
	out, err := h.d.Login.Execute(ctx, auth.LoginInput{
		Email: string(req.Body.Email), Password: req.Body.Password, IP: ci.IP, UserAgent: ci.UserAgent,
	})
	if err != nil {
		return nil, err
	}
	cookie := h.d.Cookie.refreshCookie(out.RefreshToken, out.RefreshExpiresAt, h.d.Clock.Now())
	return gen.Login200JSONResponse{
		Body:    tokenDTO(out),
		Headers: gen.Login200ResponseHeaders{SetCookie: &cookie},
	}, nil
}

// Refresh rotates the refresh cookie. The cookie alone must not be enough
// (spec 02 section 7): without X-TB-CSRF: 1 the request is refused before the
// token is looked at, so a cross-site form cannot spend or probe it.
func (h *CoreHandlers) Refresh(ctx context.Context, _ gen.RefreshRequestObject) (gen.RefreshResponseObject, error) {
	ci := ClientFrom(ctx)
	if !ci.CSRF || ci.RefreshCookie == "" {
		return nil, apperr.RefreshInvalidError()
	}
	out, err := h.d.Refresh.Execute(ctx, auth.RefreshInput{RefreshToken: ci.RefreshCookie})
	if err != nil {
		return nil, err
	}
	cookie := h.d.Cookie.refreshCookie(out.RefreshToken, out.RefreshExpiresAt, h.d.Clock.Now())
	return gen.Refresh200JSONResponse{
		Body:    tokenDTO(auth.LoginOutput(out)),
		Headers: gen.Refresh200ResponseHeaders{SetCookie: &cookie},
	}, nil
}

// Logout revokes the refresh family of the cookie and clears the cookie. It
// needs the access token (middleware) and the CSRF header like refresh; an
// unknown or missing cookie is still a success.
func (h *CoreHandlers) Logout(ctx context.Context, _ gen.LogoutRequestObject) (gen.LogoutResponseObject, error) {
	if _, err := actorOf(ctx); err != nil {
		return nil, err
	}
	ci := ClientFrom(ctx)
	if !ci.CSRF {
		return nil, apperr.TokenInvalidError()
	}
	if ci.RefreshCookie != "" {
		if err := h.d.Logout.Execute(ctx, auth.LogoutInput{RefreshToken: ci.RefreshCookie}); err != nil {
			return nil, err
		}
	}
	cookie := h.d.Cookie.clearedCookie()
	return gen.Logout204Response{Headers: gen.Logout204ResponseHeaders{SetCookie: &cookie}}, nil
}

// ChangePassword changes the own password of the authenticated user.
func (h *CoreHandlers) ChangePassword(ctx context.Context, req gen.ChangePasswordRequestObject) (gen.ChangePasswordResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.New(apperr.ValidationFailed, "body is required").WithField("body", "required")
	}
	err = h.d.ChangePassword.Execute(ctx, auth.ChangePasswordInput{
		Actor: a, RequestID: RequestIDFrom(ctx),
		CurrentPassword: req.Body.CurrentPassword, NewPassword: req.Body.NewPassword,
	})
	if err != nil {
		return nil, err
	}
	return gen.ChangePassword204Response{}, nil
}
