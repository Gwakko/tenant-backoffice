package member

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/authz"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// ListMembersInput is the request of UC-M4.
type ListMembersInput struct {
	Actor  user.ActorID
	NodeID dnode.ID
	// Inherited also lists assignments on ancestors the actor can see.
	Inherited bool
	Limit     int
	Cursor    string
}

// Validate checks the input without touching the store.
func (in ListMembersInput) Validate() error {
	if err := requireNodeID(in.NodeID); err != nil {
		return err
	}
	if in.Limit < 1 || in.Limit > maxLimit {
		return apperr.New(apperr.ValidationFailed, "limit must be 1..200").WithField("limit", "out_of_range")
	}
	_, err := decodeCursor(in.Cursor)
	return err
}

// Item is one listed member; InheritedFrom is the ancestor that holds the
// assignment, nil for an assignment on the node itself.
type Item struct {
	Member
	InheritedFrom *dnode.ID
}

// ListMembersOutput is one page of members.
type ListMembersOutput struct {
	Items      []Item
	NextCursor string
}

// ListMembers is UC-M4.
type ListMembers struct{ d Deps }

// NewListMembers builds the use case.
func NewListMembers(d Deps) *ListMembers { return &ListMembers{d: d} }

// Execute lists the members of the node ordered by email. An assignment on an
// ancestor is shown only while that ancestor is visible to the actor (EC-61);
// row-level security decides that.
func (uc *ListMembers) Execute(ctx context.Context, in ListMembersInput) (ListMembersOutput, error) {
	if err := in.Validate(); err != nil {
		return ListMembersOutput{}, err
	}
	after, _ := decodeCursor(in.Cursor)
	d := uc.d
	out := ListMembersOutput{Items: []Item{}}
	err := d.Runner.InTx(ctx, in.Actor, readTx, func(ctx context.Context, tx port.Tx) error {
		if _, err := authz.Require(ctx, d.Checker, tx, in.Actor, in.NodeID, role.MemberRead); err != nil {
			return err
		}
		page, err := d.Members.List(ctx, tx, in.NodeID, in.Inherited, after, in.Limit+1)
		if err != nil {
			return err
		}
		if len(page) > in.Limit {
			page = page[:in.Limit]
			last := page[len(page)-1]
			out.NextCursor = encodeCursor(port.MemberCursor{Email: last.Email.String(), UserID: last.UserID, NodeID: last.NodeID})
		}
		for _, m := range page {
			it := Item{Member: fromPort(m)}
			if m.NodeID != in.NodeID {
				from := m.NodeID
				it.InheritedFrom = &from
			}
			out.Items = append(out.Items, it)
		}
		return nil
	})
	if err != nil {
		return ListMembersOutput{}, err
	}
	return out, nil
}
