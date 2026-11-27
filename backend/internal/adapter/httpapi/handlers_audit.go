package httpapi

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/audit"
)

// ListAudit lists the journal entries in the subtree of a scope node. The
// cursor is bound to the scope, the filters and the reader (EC-62).
func (h *LedgerHandlers) ListAudit(ctx context.Context, req gen.ListAuditRequestObject) (gen.ListAuditResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	p := req.Params
	limit, err := ParseLimit(p.Limit)
	if err != nil {
		return nil, err
	}
	action := ""
	if p.Action != nil {
		action = string(*p.Action)
	}
	bind := CursorBinding{
		Scope: scopeAudit + ":" + p.ScopeNodeId.String(),
		Filters: map[string]string{
			"actor": actorString(a), "node": optUUID(p.NodeId), "action": action, "by": optUUID(p.ActorId),
			"from": optTime(p.From), "to": optTime(p.To),
		},
	}
	after, err := h.d.Cursors.decodePageCursor(p.Cursor, bind)
	if err != nil {
		return nil, err
	}
	in := audit.ListAuditInput{
		Actor: a, ScopeNodeID: nodeID(p.ScopeNodeId), From: p.From, To: p.To, Limit: limit, Cursor: after,
	}
	if p.NodeId != nil {
		n := nodeID(*p.NodeId)
		in.NodeID = &n
	}
	if p.Action != nil {
		in.Action = &action
	}
	if p.ActorId != nil {
		u := userID(*p.ActorId)
		in.ActorID = &u
	}
	out, err := h.d.ListAudit.Execute(ctx, in)
	if err != nil {
		return nil, err
	}
	next, err := h.d.Cursors.encodePageCursor(out.NextCursor, bind)
	if err != nil {
		return nil, err
	}
	items := make([]gen.AuditEntry, len(out.Items))
	for i, r := range out.Items {
		items[i] = auditEntryDTO(r)
	}
	return gen.ListAudit200JSONResponse{Body: gen.AuditPage{Items: items, NextCursor: next}}, nil
}
