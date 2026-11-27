package apperr

// Codes of idempotent requests (spec 03 section 4).
const (
	// IdempotencyKeyRequired means the Idempotency-Key header is absent.
	IdempotencyKeyRequired Code = "idempotency_key_required"
	// IdempotencyKeyInvalid means the key is empty, too long or has characters
	// outside [A-Za-z0-9_:.-].
	IdempotencyKeyInvalid Code = "idempotency_key_invalid"
	// IdempotencyKeyReused means the key was already used for another request.
	IdempotencyKeyReused Code = "idempotency_key_reused"
)
