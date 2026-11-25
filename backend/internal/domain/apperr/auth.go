package apperr

// Codes of authentication. They carry no detail on purpose: the client learns
// that a credential or a token was rejected, never why.
const (
	InvalidCredentials Code = "invalid_credentials" //nolint:gosec // an error code, not a credential
	RefreshInvalid     Code = "refresh_invalid"
	TokenInvalid       Code = "token_invalid"
	TokenExpired       Code = "token_expired"
)

// InvalidCredentialsError is the single answer of a failed login: an unknown
// email, a wrong password and an inactive account look the same.
func InvalidCredentialsError() Error {
	return New(InvalidCredentials, "invalid credentials")
}

// RefreshInvalidError means the refresh token is absent, malformed, expired,
// revoked or already used.
func RefreshInvalidError() Error {
	return New(RefreshInvalid, "refresh token is not valid")
}

// TokenInvalidError means the access token failed verification or is stale.
func TokenInvalidError() Error {
	return New(TokenInvalid, "access token is not valid")
}

// TokenExpiredError means the access token is past its expiry.
func TokenExpiredError() Error {
	return New(TokenExpired, "access token has expired")
}
