// Package report holds the use cases of reports (UC-R1, UC-R2). Both run in one
// REPEATABLE READ READ ONLY transaction: all the queries of a report see the
// same snapshot, so a move or a new operation committed meanwhile is either
// fully visible in the report or not at all. Both need report.read on the
// scope node; a node outside the actor's subtrees is not_found, the same
// answer as for a missing one.
package report

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/authz"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Deps are the ports every report use case works through.
type Deps struct {
	Runner  port.TxRunner
	Checker authz.Checker
	Reports port.ReportRepo
}

// snapshotTx is the transaction of a report. Without REPEATABLE READ the
// queries of one report could see different states of the tree (EC-85).
var snapshotTx = port.TxOptions{ReadOnly: true, Isolation: port.RepeatableRead}

const maxLimit = 200

func (d Deps) require(ctx context.Context, tx port.Tx, actor user.ActorID, id dnode.ID) error {
	_, err := authz.Require(ctx, d.Checker, tx, actor, id, role.ReportRead)
	return err
}

func requireID(id dnode.ID) error {
	if id.IsZero() {
		return apperr.New(apperr.ValidationFailed, "node_id is required").WithField("node_id", "required")
	}
	return nil
}

// encodeCursor and decodeCursor turn the keyset position into an opaque
// string. The position carries no rights: what a page shows is decided by the
// actor's visibility on every request.
func encodeCursor(c port.ChildCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.NameKey + "\x00" + c.ID.String()))
}

func decodeCursor(s string) (*port.ChildCursor, error) {
	if s == "" {
		return nil, nil
	}
	bad := apperr.InvalidCursorError()
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, bad
	}
	key, id, ok := strings.Cut(string(raw), "\x00")
	if !ok {
		return nil, bad
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, bad
	}
	return &port.ChildCursor{NameKey: key, ID: dnode.ID(u)}, nil
}
