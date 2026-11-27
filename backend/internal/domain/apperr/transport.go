package apperr

// Codes of the transport layer (spec 03 section 2.3). They describe a request
// that never reached a use case, or a failure that is none of the use case's
// business. Their messages carry no detail on purpose.
const (
	TokenMissing         Code = "token_missing"
	InvalidCursor        Code = "invalid_cursor"
	InvalidPeriod        Code = "invalid_period"
	PayloadTooLarge      Code = "payload_too_large"
	UnsupportedMediaType Code = "unsupported_media_type"
	RateLimited          Code = "rate_limited"
	Internal             Code = "internal"
)

// TokenMissingError means the request carries no bearer token.
func TokenMissingError() Error {
	return New(TokenMissing, "access token is missing")
}

// InvalidCursorError means the pagination cursor is damaged, forged, expired or
// belongs to other filters or another scope.
func InvalidCursorError() Error {
	return New(InvalidCursor, "cursor is not valid")
}

// InvalidPeriodError means the report period or its parameters are not valid.
func InvalidPeriodError() Error {
	return New(InvalidPeriod, "report period is not valid")
}

// PayloadTooLargeError means the request body exceeds the limit.
func PayloadTooLargeError() Error {
	return New(PayloadTooLarge, "request body is too large")
}

// UnsupportedMediaTypeError means the request body is not application/json.
func UnsupportedMediaTypeError() Error {
	return New(UnsupportedMediaType, "content type must be application/json")
}

// RateLimitedError means the caller exceeded a rate limit.
func RateLimitedError() Error {
	return New(RateLimited, "too many requests")
}

// InternalError is an unexpected failure; the cause stays in the log.
func InternalError() Error {
	return New(Internal, "internal error")
}
