package port

import (
	"context"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// NewUser is a user to create together with a first role assignment.
type NewUser struct {
	ID        user.ID
	Email     user.Email
	Hash      user.PasswordHash
	CreatedAt time.Time
}

// UserRepo creates users and looks up the ones a member operation touches.
type UserRepo interface {
	// IDByEmail finds a user by normalized email, also one that row-level
	// security hides from the actor (a member of another branch).
	IDByEmail(ctx context.Context, tx Tx, email user.Email) (id user.ID, found bool, err error)
	// Insert creates the user with the id the caller generated and does not read
	// it back: until the user has a visible assignment it is invisible to the
	// creator. It reports false, without error, when the email is taken.
	Insert(ctx context.Context, tx Tx, u NewUser) (inserted bool, err error)
	// Status returns the status of a user the actor can see (the user has an
	// assignment on a visible node); an invisible user is apperr.NotFound.
	Status(ctx context.Context, tx Tx, id user.ID) (user.Status, error)
}
