package httpapi

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

func missingBody() error {
	return apperr.New(apperr.ValidationFailed, "body is required").WithField("body", "required")
}

// GetNode returns one node.
func (h *CoreHandlers) GetNode(ctx context.Context, req gen.GetNodeRequestObject) (gen.GetNodeResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := h.d.GetNode.Execute(ctx, node.GetNodeInput{Actor: a, NodeID: nodeID(req.Id)})
	if err != nil {
		return nil, err
	}
	return gen.GetNode200JSONResponse{Body: nodeDTO(out.Node)}, nil
}

// ListChildren lists the direct children of a node.
func (h *CoreHandlers) ListChildren(ctx context.Context, req gen.ListChildrenRequestObject) (gen.ListChildrenResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	limit, err := ParseLimit(req.Params.Limit)
	if err != nil {
		return nil, err
	}
	status := "all"
	if req.Params.Status != nil {
		status = string(*req.Params.Status)
	}
	q := deref(req.Params.Q, "")
	bind := CursorBinding{Scope: scopeChildren + ":" + req.Id.String(), Filters: map[string]string{"status": status, "q": q}}
	after, err := h.d.Cursors.decodePageCursor(req.Params.Cursor, bind)
	if err != nil {
		return nil, err
	}
	out, err := h.d.ListChildren.Execute(ctx, node.ListChildrenInput{
		Actor: a, NodeID: nodeID(req.Id), Status: status, Query: q, Limit: limit, Cursor: after,
	})
	if err != nil {
		return nil, err
	}
	next, err := h.d.Cursors.encodePageCursor(out.NextCursor, bind)
	if err != nil {
		return nil, err
	}
	return gen.ListChildren200JSONResponse{Body: gen.NodeSummaryPage{Items: summaryDTOs(out.Items), NextCursor: next}}, nil
}

// CreateChild creates a child node.
func (h *CoreHandlers) CreateChild(ctx context.Context, req gen.CreateChildRequestObject) (gen.CreateChildResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, missingBody()
	}
	out, err := h.d.CreateNode.Execute(ctx, node.CreateNodeInput{
		Actor: a, RequestID: RequestIDFrom(ctx), ParentID: nodeID(req.Id),
		Type: string(req.Body.Type), Name: req.Body.Name,
	})
	if err != nil {
		return nil, err
	}
	return gen.CreateChild201JSONResponse{Body: nodeDTO(out.Node)}, nil
}

// RenameNode renames a node.
func (h *CoreHandlers) RenameNode(ctx context.Context, req gen.RenameNodeRequestObject) (gen.RenameNodeResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, missingBody()
	}
	out, err := h.d.RenameNode.Execute(ctx, node.RenameNodeInput{
		Actor: a, RequestID: RequestIDFrom(ctx), NodeID: nodeID(req.Id), Name: req.Body.Name,
	})
	if err != nil {
		return nil, err
	}
	return gen.RenameNode200JSONResponse{Body: nodeDTO(out.Node)}, nil
}

// MoveNode moves a node under another parent.
func (h *CoreHandlers) MoveNode(ctx context.Context, req gen.MoveNodeRequestObject) (gen.MoveNodeResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, missingBody()
	}
	out, err := h.d.MoveNode.Execute(ctx, node.MoveNodeInput{
		Actor: a, RequestID: RequestIDFrom(ctx), NodeID: nodeID(req.Id), NewParentID: nodeID(req.Body.NewParentId),
	})
	if err != nil {
		return nil, err
	}
	return gen.MoveNode200JSONResponse{Body: nodeDTO(out.Node)}, nil
}

// ListMoveTargets lists the parents a node could be moved to.
func (h *CoreHandlers) ListMoveTargets(ctx context.Context, req gen.ListMoveTargetsRequestObject) (gen.ListMoveTargetsResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	limit, err := ParseLimit(req.Params.Limit)
	if err != nil {
		return nil, err
	}
	q := deref(req.Params.Q, "")
	bind := CursorBinding{Scope: scopeTargets + ":" + req.Id.String(), Filters: map[string]string{"q": q}}
	after, err := h.d.Cursors.decodePageCursor(req.Params.Cursor, bind)
	if err != nil {
		return nil, err
	}
	out, err := h.d.ListMoveTargets.Execute(ctx, node.ListMoveTargetsInput{
		Actor: a, NodeID: nodeID(req.Id), Query: q, Limit: limit, Cursor: after,
	})
	if err != nil {
		return nil, err
	}
	next, err := h.d.Cursors.encodePageCursor(out.NextCursor, bind)
	if err != nil {
		return nil, err
	}
	return gen.ListMoveTargets200JSONResponse{Body: gen.NodeSummaryPage{Items: summaryDTOs(out.Items), NextCursor: next}}, nil
}

// DeactivateNode marks a node inactive.
func (h *CoreHandlers) DeactivateNode(ctx context.Context, req gen.DeactivateNodeRequestObject) (gen.DeactivateNodeResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := h.d.DeactivateNode.Execute(ctx, node.SetStatusInput{Actor: a, RequestID: RequestIDFrom(ctx), NodeID: nodeID(req.Id)})
	if err != nil {
		return nil, err
	}
	return gen.DeactivateNode200JSONResponse{Body: nodeDTO(out.Node)}, nil
}

// ActivateNode marks a node active.
func (h *CoreHandlers) ActivateNode(ctx context.Context, req gen.ActivateNodeRequestObject) (gen.ActivateNodeResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := h.d.ActivateNode.Execute(ctx, node.SetStatusInput{Actor: a, RequestID: RequestIDFrom(ctx), NodeID: nodeID(req.Id)})
	if err != nil {
		return nil, err
	}
	return gen.ActivateNode200JSONResponse{Body: nodeDTO(out.Node)}, nil
}

// DeleteNode deletes a node without children, balance and history.
func (h *CoreHandlers) DeleteNode(ctx context.Context, req gen.DeleteNodeRequestObject) (gen.DeleteNodeResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := h.d.DeleteNode.Execute(ctx, node.DeleteNodeInput{Actor: a, RequestID: RequestIDFrom(ctx), NodeID: nodeID(req.Id)}); err != nil {
		return nil, err
	}
	return gen.DeleteNode204Response{}, nil
}
