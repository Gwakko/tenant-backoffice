package httpapi

import (
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
)

func memberDTO(m member.Member) gen.Member {
	return gen.Member{
		UserId: uid(m.UserID), Email: gen.Email(m.Email), Role: gen.Role(m.Role),
		NodeId: uid(m.NodeID), GrantedAt: m.GrantedAt.UTC(),
	}
}

func memberItemDTO(it member.Item) gen.Member {
	out := memberDTO(it.Member)
	if it.InheritedFrom != nil {
		f := uid(*it.InheritedFrom)
		out.InheritedFrom = &f
	}
	return out
}
