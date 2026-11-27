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
	if n.ParentID != nil {
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
