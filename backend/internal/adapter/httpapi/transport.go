package httpapi

import (
	"context"
	"net/http"
)

const (
	// RefreshCookieName is the cookie that carries the refresh token.
	RefreshCookieName = "tb_refresh"
	// CSRFHeader must carry CSRFValue on the requests that the refresh cookie
	// authenticates (spec 02 section 7).
	CSRFHeader = "X-TB-CSRF"
	// CSRFValue is the only accepted value of CSRFHeader.
	CSRFValue = "1"
	// RefreshCookiePath limits the cookie to the auth routes.
	RefreshCookiePath = apiPrefix + "/auth"
)

// ClientInfo is the part of the raw request the strict handlers cannot see
// (the generated request objects carry only declared parameters).
type ClientInfo struct {
	// IP is the client address as the rate limiter counts it.
	IP        string
	UserAgent string
	// RefreshCookie is the value of the tb_refresh cookie, "" when absent.
	RefreshCookie string
	// CSRF is true when X-TB-CSRF carries exactly "1".
	CSRF bool
}

// ClientFrom returns the client information of the request.
func ClientFrom(ctx context.Context) ClientInfo { return stateOf(ctx).client }

// maxUserAgent bounds the stored user agent.
const maxUserAgent = 256

// capture records the client information in the request state.
func capture(trustXFF bool) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ci := ClientInfo{IP: clientIP(r, trustXFF), UserAgent: r.UserAgent()}
			if len(ci.UserAgent) > maxUserAgent {
				ci.UserAgent = ci.UserAgent[:maxUserAgent]
			}
			if c, err := r.Cookie(RefreshCookieName); err == nil {
				ci.RefreshCookie = c.Value
			}
			vals := r.Header.Values(CSRFHeader)
			ci.CSRF = len(vals) == 1 && vals[0] == CSRFValue
			stateOf(r.Context()).client = ci
			next.ServeHTTP(w, r)
		})
	}
}
