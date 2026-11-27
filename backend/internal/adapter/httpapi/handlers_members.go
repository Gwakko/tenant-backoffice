package httpapi

import (
	"context"
	"strconv"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
)

// ListMembers lists the role assignments of a node.
func (h *CoreHandlers) ListMembers(ctx context.Context, req gen.ListMembersRequestObject) (gen.ListMembersResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	limit, err := ParseLimit(req.Params.Limit)
	if err != nil {
		return nil, err
	}
	inherited := deref(req.Params.Inherited, false)
	bind := CursorBinding{Scope: scopeMembers + ":" + req.Id.String(), Filters: map[string]string{"inherited": strconv.FormatBool(inherited)}}
	after, err := h.d.Cursors.decodePageCursor(req.Params.Cursor, bind)
	if err != nil {
		return nil, err
	}
	out, err := h.d.ListMembers.Execute(ctx, member.ListMembersInput{
		Actor: a, NodeID: nodeID(req.Id), Inherited: inherited, Limit: limit, Cursor: after,
	})
	if err != nil {
		return nil, err
	}
	next, err := h.d.Cursors.encodePageCursor(out.NextCursor, bind)
	if err != nil {
		return nil, err
	}
	items := make([]gen.Member, len(out.Items))
	for i, it := range out.Items {
		items[i] = memberItemDTO(it)
	}
	return gen.ListMembers200JSONResponse{Body: gen.MemberPage{Items: items, NextCursor: next}}, nil
}

// AddMember grants a role; the answer has the same shape whether the user
// existed or was created.
func (h *CoreHandlers) AddMember(ctx context.Context, req gen.AddMemberRequestObject) (gen.AddMemberResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, missingBody()
	}
	out, err := h.d.AddMember.Execute(ctx, member.AddMemberInput{
		Actor: a, RequestID: RequestIDFrom(ctx), NodeID: nodeID(req.Id),
		Email: string(req.Body.Email), Role: string(req.Body.Role),
		InitialPassword: deref(req.Body.InitialPassword, ""),
	})
	if err != nil {
		return nil, err
	}
	return gen.AddMember201JSONResponse{Body: memberDTO(out.Member)}, nil
}

// ChangeMemberRole replaces the role of an assignment on this node.
func (h *CoreHandlers) ChangeMemberRole(ctx context.Context, req gen.ChangeMemberRoleRequestObject) (gen.ChangeMemberRoleResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, missingBody()
	}
	out, err := h.d.ChangeRole.Execute(ctx, member.ChangeMemberRoleInput{
		Actor: a, RequestID: RequestIDFrom(ctx), NodeID: nodeID(req.Id), UserID: userID(req.UserId), Role: string(req.Body.Role),
	})
	if err != nil {
		return nil, err
	}
	return gen.ChangeMemberRole200JSONResponse{Body: memberDTO(out.Member)}, nil
}

// RemoveMember removes an assignment from this node.
func (h *CoreHandlers) RemoveMember(ctx context.Context, req gen.RemoveMemberRequestObject) (gen.RemoveMemberResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	err = h.d.RemoveMember.Execute(ctx, member.RemoveMemberInput{
		Actor: a, RequestID: RequestIDFrom(ctx), NodeID: nodeID(req.Id), UserID: userID(req.UserId),
	})
	if err != nil {
		return nil, err
	}
	return gen.RemoveMember204Response{}, nil
}
