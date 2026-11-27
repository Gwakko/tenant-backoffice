package httpapi

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

// GetMe returns the acting user and the access roots.
func (h *CoreHandlers) GetMe(ctx context.Context, _ gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	p, err := h.d.Profile.Execute(ctx, authProfileInput(a))
	if err != nil {
		return nil, err
	}
	roots, err := h.d.AccessRoots.Execute(ctx, node.ListAccessRootsInput{Actor: a})
	if err != nil {
		return nil, err
	}
	return gen.GetMe200JSONResponse{Body: gen.Me{
		Id: uid(p.ID), Email: gen.Email(p.Email), AccessRoots: rolesDTO(roots.Roots),
	}}, nil
}

// GetMyPermissions returns the role and permissions on a node.
func (h *CoreHandlers) GetMyPermissions(ctx context.Context, req gen.GetMyPermissionsRequestObject) (gen.GetMyPermissionsResponseObject, error) {
	a, err := actorOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := h.d.Permissions.Execute(ctx, node.GetPermissionsInput{Actor: a, NodeID: nodeID(req.Params.NodeId)})
	if err != nil {
		return nil, err
	}
	perms := make([]gen.Permission, len(out.Permissions))
	for i, p := range out.Permissions {
		perms[i] = gen.Permission(p)
	}
	return gen.GetMyPermissions200JSONResponse{Body: gen.NodePermissions{
		NodeId: req.Params.NodeId, Role: gen.Role(out.Role), Permissions: perms,
	}}, nil
}
