package httpapi

import (
	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
)

// uid converts the typed ids of the domain (node.ID, user.ID) to a UUID.
func uid[T ~[16]byte](id T) uuid.UUID { return uuid.UUID(id) }

// Name of the operations that list a page, used in the cursor binding.
const (
	scopeChildren = "children"
	scopeTargets  = "move-targets"
	scopeMembers  = "members"
)

// decodePageCursor turns the signed cursor of the request into the position
// string the use case understands. No cursor is the first page. The binding
// holds the listing and its filters, so a cursor cannot be reused elsewhere.
func (c *CursorCodec) decodePageCursor(cursor *gen.Cursor, b CursorBinding) (string, error) {
	if cursor == nil || *cursor == "" {
		return "", nil
	}
	k, err := c.Decode(*cursor, b)
	if err != nil {
		return "", err
	}
	if len(k) != 1 {
		return "", apperr.InvalidCursorError()
	}
	return k[0], nil
}

// encodePageCursor signs the position the use case returned; "" is the last page.
func (c *CursorCodec) encodePageCursor(next string, b CursorBinding) (*string, error) {
	if next == "" {
		return nil, nil
	}
	tok, err := c.Encode([]string{next}, b)
	if err != nil {
		return nil, err
	}
	return &tok, nil
}

func deref[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

func nodeID(id uuid.UUID) dnode.ID { return dnode.ID(id) }
func userID(id uuid.UUID) user.ID  { return user.ID(id) }

func authProfileInput(a user.ActorID) auth.GetProfileInput { return auth.GetProfileInput{Actor: a} }
