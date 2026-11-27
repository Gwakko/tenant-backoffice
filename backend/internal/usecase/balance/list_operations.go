package balance

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dbalance "github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// maxPageLimit is the largest page of a listing (spec 03 section 3).
const maxPageLimit = 200

// ListOperationsInput is the request of UC-B5.
type ListOperationsInput struct {
	Actor user.ActorID
	// ScopeNodeID is the node whose subtree is listed; operation.read is
	// required on it.
	ScopeNodeID dnode.ID
	// NodeID narrows the listing to one node, which must lie in the subtree of
	// the scope; otherwise the answer is not_found.
	NodeID *dnode.ID
	// Kinds is an OR filter; empty means every kind.
	Kinds []string
	// From is inclusive, To exclusive.
	From, To *time.Time
	Limit    int
	// Cursor is the position that a previous page returned, "" for the first.
	Cursor string
}

// Validate checks the input without touching the store.
func (in ListOperationsInput) Validate() error {
	if in.ScopeNodeID.IsZero() {
		return validation("scope_node_id", "required", "scope_node_id is required")
	}
	if in.NodeID != nil && in.NodeID.IsZero() {
		return validation("node_id", "invalid", "node_id is not valid")
	}
	for _, k := range in.Kinds {
		if _, err := dbalance.ParseKind(k); err != nil {
			return validation("kind", "unknown", "unknown operation kind")
		}
	}
	if in.From != nil && in.To != nil && !in.From.Before(*in.To) {
		return validation("from", "after_to", "from must be before to")
	}
	if in.Limit < 1 || in.Limit > maxPageLimit {
		return validation("limit", "out_of_range", "limit must be 1..200")
	}
	_, err := decodeOperationCursor(in.Cursor)
	return err
}

// OperationItem is an operation with the name of its node.
type OperationItem struct {
	Operation dbalance.Operation
	NodeName  string
}

// ListOperationsOutput is one page of the ledger, newest first.
type ListOperationsOutput struct {
	Items      []OperationItem
	NextCursor string
}

// ListOperations is UC-B5. It is read only.
type ListOperations struct{ d Deps }

// NewListOperations builds the use case.
func NewListOperations(d Deps) *ListOperations { return &ListOperations{d: d} }

// Execute requires operation.read on the scope node (a node the actor cannot
// see is not_found) and returns the operations of the scope's subtree that the
// actor's row-level visibility shows, ordered by (created_at, id) descending.
func (uc *ListOperations) Execute(ctx context.Context, in ListOperationsInput) (ListOperationsOutput, error) {
	if err := in.Validate(); err != nil {
		return ListOperationsOutput{}, err
	}
	after, _ := decodeOperationCursor(in.Cursor)
	f := port.OperationFilter{ScopeNodeID: in.ScopeNodeID, NodeID: in.NodeID, From: in.From, To: in.To}
	seen := map[dbalance.Kind]bool{}
	for _, k := range in.Kinds {
		kind := dbalance.Kind(k)
		if !seen[kind] {
			seen[kind] = true
			f.Kinds = append(f.Kinds, kind)
		}
	}
	d := uc.d
	out := ListOperationsOutput{Items: []OperationItem{}}
	err := d.Runner.InTx(ctx, in.Actor, readTx, func(ctx context.Context, tx port.Tx) error {
		if err := d.require(ctx, tx, in.Actor, in.ScopeNodeID, role.OperationRead); err != nil {
			return err
		}
		if in.NodeID != nil {
			inside, err := d.Nodes.IsInSubtree(ctx, tx, *in.NodeID, in.ScopeNodeID)
			if err != nil {
				return err
			}
			if !inside {
				return apperr.NotFoundError("node")
			}
		}
		page, err := d.Ops.List(ctx, tx, f, after, in.Limit+1)
		if err != nil {
			return err
		}
		if len(page) > in.Limit {
			page = page[:in.Limit]
			last := page[len(page)-1].Operation
			out.NextCursor = encodeOperationCursor(port.OperationCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		}
		for _, r := range page {
			out.Items = append(out.Items, OperationItem{Operation: r.Operation, NodeName: r.NodeName})
		}
		return nil
	})
	if err != nil {
		return ListOperationsOutput{}, err
	}
	return out, nil
}

// encodeOperationCursor and decodeOperationCursor turn the keyset position
// into an opaque string. The position carries no rights: what a page shows is
// decided by the actor's visibility on every request.
func encodeOperationCursor(c port.OperationCursor) string {
	return strconv.FormatInt(c.CreatedAt.UnixMicro(), 10) + "_" + uuid.UUID(c.ID).String()
}

func decodeOperationCursor(s string) (*port.OperationCursor, error) {
	if s == "" {
		return nil, nil
	}
	ts, id, ok := strings.Cut(s, "_")
	if !ok {
		return nil, apperr.InvalidCursorError()
	}
	micro, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, apperr.InvalidCursorError()
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, apperr.InvalidCursorError()
	}
	return &port.OperationCursor{CreatedAt: time.UnixMicro(micro).UTC(), ID: dbalance.OperationID(u)}, nil
}
