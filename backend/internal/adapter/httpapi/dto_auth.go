package httpapi

import (
	"net/http"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

// refreshCookie is the Set-Cookie value that carries a new refresh token. The
// attributes are fixed: HttpOnly keeps the token away from scripts,
// SameSite=Strict keeps it off cross-site requests, the path limits it to the
// auth routes. Secure is configurable for local plain-HTTP work.
func (c CookieConfig) refreshCookie(token string, expires, now time.Time) string {
	maxAge := max(int(expires.Sub(now).Seconds()), 1)
	//nolint:gosec // Secure comes from the config (on by default); HttpOnly and SameSite=Strict are fixed.
	return (&http.Cookie{
		Name: RefreshCookieName, Value: token, Path: RefreshCookiePath,
		Expires: expires.UTC(), MaxAge: maxAge,
		HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteStrictMode,
	}).String()
}

// clearedCookie expires the refresh cookie in the browser.
func (c CookieConfig) clearedCookie() string {
	//nolint:gosec // Secure comes from the config (on by default); HttpOnly and SameSite=Strict are fixed.
	return (&http.Cookie{
		Name: RefreshCookieName, Value: "", Path: RefreshCookiePath,
		MaxAge: -1, Expires: time.Unix(0, 0).UTC(),
		HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteStrictMode,
	}).String()
}

// tokenDTO is the body of login and refresh. The refresh token is never in it:
// it travels only in the cookie.
func tokenDTO(out auth.LoginOutput) gen.TokenResponse {
	return gen.TokenResponse{
		AccessToken: out.AccessToken,
		ExpiresIn:   int(out.ExpiresIn.Seconds()),
		User:        gen.UserRef{Id: uid(out.UserID), Email: gen.Email(out.Email)},
	}
}

func rolesDTO(roots []node.AccessRoot) []gen.AccessRoot {
	out := make([]gen.AccessRoot, len(roots))
	for i, r := range roots {
		out[i] = gen.AccessRoot{NodeId: uid(r.NodeID), Name: r.Name, Type: gen.NodeType(r.Type), Role: gen.Role(r.Role)}
	}
	return out
}
