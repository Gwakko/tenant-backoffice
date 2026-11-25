package user

import (
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

// ID identifies a user.
type ID uuid.UUID

// ActorID is the ID of the authenticated user performing an operation.
type ActorID = ID

// ParseID parses the canonical textual form of a user ID.
func ParseID(s string) (ID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return ID{}, apperr.New(apperr.InvalidID, "user id is not a valid UUID")
	}
	return ID(u), nil
}

// String returns the canonical textual form.
func (id ID) String() string { return uuid.UUID(id).String() }

// IsZero reports whether id is the zero value.
func (id ID) IsZero() bool { return id == ID{} }

// Status is the activity state of a user.
type Status string

// User statuses.
const (
	StatusActive   Status = "active"
	StatusInactive Status = "inactive"
)

// User is a backoffice account. A user without role assignments can sign in
// but sees no nodes.
type User struct {
	ID           ID
	Email        Email
	PasswordHash PasswordHash
	Status       Status
	TokenVersion int64
	CreatedAt    time.Time
}

// IsActive reports whether the user may authenticate.
func (u User) IsActive() bool { return u.Status == StatusActive }
