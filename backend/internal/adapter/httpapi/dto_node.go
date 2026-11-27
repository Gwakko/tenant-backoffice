package httpapi

import (
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

// nodeDTO maps a node view to the contract type. Times are UTC.
func nodeDTO(v node.View) gen.Node {
	crumbs := make([]gen.Breadcrumb, len(v.Breadcrumbs))
	for i, c := range v.Breadcrumbs {
		crumbs[i] = gen.Breadcrumb{Id: uid(c.ID), Name: c.Name, Type: gen.NodeType(c.Type)}
	}
	n := v.Node
	out := gen.Node{
		Id: uid(n.ID), Type: gen.NodeType(n.Type), Name: n.Name.String(), Status: gen.NodeStatus(n.Status),
		EffectiveActive: v.EffectiveActive, ChildrenCount: v.ChildrenCount, Breadcrumbs: crumbs,
		CreatedAt: n.CreatedAt.UTC(), UpdatedAt: n.UpdatedAt.UTC(),
	}
	// The breadcrumbs end at the node and start at the actor's access root. A
	// node that is the access root has a parent the actor may not see, and its
	// id is not revealed either (spec 03 GET /nodes/{id}, EC-30): parentId is null.
	if n.ParentID != nil && len(v.Breadcrumbs) > 1 {
		p := uid(*n.ParentID)
		out.ParentId = &p
	}
	return out
}

func summaryDTOs(items []node.Summary) []gen.NodeSummary {
	out := make([]gen.NodeSummary, len(items))
	for i, s := range items {
		out[i] = gen.NodeSummary{
			Id: uid(s.ID), Type: gen.NodeType(s.Type), Name: s.Name,
			Status: gen.NodeStatus(s.Status), ChildrenCount: s.ChildrenCount,
		}
	}
	return out
}
