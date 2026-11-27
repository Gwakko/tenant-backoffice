package httpapi

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/balance"
)

// Names of the listings that sign a cursor.
const (
	scopeOperations = "operations"
	scopeAudit      = "audit"
)

// ListOperations lists the operations in the subtree of a scope node. The
// cursor is bound to the scope, the node filter, the kinds, the period and the
// reader, so it cannot be carried to another listing (EC-62).
func (h *LedgerHandlers) ListOperations(ctx context.Context, req gen.ListOperationsRequestObject) (gen.ListOperationsResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	limit, err := ParseLimit(p.Limit)
	if err != nil {
		return nil, err
	}
	var kinds []string
	if p.Kind != nil {
		for _, k := range *p.Kind {
			kinds = append(kinds, string(k))
		}
	}
	bind := CursorBinding{
		Scope: scopeOperations + ":" + p.ScopeNodeId.String(),
		Filters: map[string]string{
			"actor": actorString(a), "node": optUUID(p.NodeId), "kinds": canonicalKinds(kinds),
			"from": optTime(p.From), "to": optTime(p.To),
		},
	}
	after, err := h.d.Cursors.decodePageCursor(p.Cursor, bind)
	if err != nil {
		return nil, err
	}
	in := balance.ListOperationsInput{
		Actor: a, ScopeNodeID: nodeID(p.ScopeNodeId), Kinds: kinds, From: p.From, To: p.To, Limit: limit, Cursor: after,
	}
	if p.NodeId != nil {
		n := nodeID(*p.NodeId)
		in.NodeID = &n
	}
	out, err := h.d.ListOperations.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	next, err := h.d.Cursors.encodePageCursor(out.NextCursor, bind)
	if err != nil {
		return nil, err
	}
	items := make([]gen.OperationListItem, len(out.Items))
	for i, it := range out.Items {
		items[i] = operationListItemDTO(it.Operation, it.NodeName)
	}
	return gen.ListOperations200JSONResponse{Body: gen.OperationPage{Items: items, NextCursor: next}}, nil
}

// GetOperation returns one ledger record.
func (h *LedgerHandlers) GetOperation(ctx context.Context, req gen.GetOperationRequestObject) (gen.GetOperationResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := h.d.GetOperation.Execute(ctx, balance.GetOperationInput{Actor: a, OperationID: operationID(req.Id)})
	if err != nil {
		return nil, err
	}
	return gen.GetOperation200JSONResponse{Body: operationDTO(out.Operation)}, nil
}

func optUUID(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func optTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// canonicalKinds makes the kind filter order- and duplicate-insensitive, so
// the same set of kinds binds the same cursor.
func canonicalKinds(kinds []string) string {
	k := slices.Clone(kinds)
	slices.Sort(k)
	return strings.Join(slices.Compact(k), ",")
}

func actorString(a user.ActorID) string { return uuid.UUID(a).String() }
