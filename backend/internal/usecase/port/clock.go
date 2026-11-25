package port

import (
	"time"

	"github.com/google/uuid"
)

// Clock is the source of the current time, always UTC.
type Clock interface {
	Now() time.Time
}

// IDGen generates identifiers (UUIDv7) in the application, so that rows can be
// inserted without RETURNING.
type IDGen interface {
	New() uuid.UUID
}
