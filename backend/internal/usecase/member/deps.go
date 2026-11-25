// Package member holds the use cases of node members (UC-M1..M4). The database
// does not check which role may do what with assignments (row-level security
// only limits them to the actor's subtrees), so these use cases are the only
// guard: member.manage on the node on every call, and CanGrant / CanRevoke on
// the role being assigned or removed. A node outside the actor's subtrees is
// not_found, one inside them without the right is forbidden.
package member

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/authz"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Deps are the ports every member use case works through.
type Deps struct {
	Runner  port.TxRunner
	Checker authz.Checker
	Nodes   port.NodeRepo
	Members port.MemberRepo
	Users   port.UserRepo
	Hasher  user.Hasher
	Audit   port.AuditWriter
	Clock   port.Clock
	IDs     port.IDGen
}

// Member is an assignment as shown to a client. The same shape is returned
// whether the user existed before or was created by the request.
type Member struct {
	UserID    user.ID
	Email     user.Email
	Role      role.Role
	NodeID    dnode.ID
	GrantedAt time.Time
}

func fromPort(m port.Member) Member {
	return Member{UserID: m.UserID, Email: m.Email, Role: m.Role, NodeID: m.NodeID, GrantedAt: m.GrantedAt}
}

var (
	readTx  = port.TxOptions{ReadOnly: true}
	writeTx = port.TxOptions{}
)

// requireManage checks member.manage on the node and returns the actor's
// effective role there, which CanGrant and CanRevoke compare against.
func (d Deps) requireManage(ctx context.Context, tx port.Tx, actor user.ActorID, id dnode.ID) (role.Role, error) {
	return authz.Require(ctx, d.Checker, tx, actor, id, role.MemberManage)
}

func notGrantable() error {
	return apperr.New(apperr.RoleNotGrantable, "the role of the actor does not allow this assignment")
}

// isRoot reports whether the node is the root of the tree. It is read right
// after the authorization and before any row lock: a request that waits for a
// lock may lose its own visibility of the tree meanwhile (EC-36), after which
// the node can no longer be read.
func (d Deps) isRoot(ctx context.Context, tx port.Tx, nodeID dnode.ID) (bool, error) {
	n, err := d.Nodes.Get(ctx, tx, nodeID)
	if err != nil {
		return false, err
	}
	return n.ParentID == nil, nil
}

// guardLastRootOwner refuses to take owner away from the last active owner of
// the root node (EC-35). Other nodes may lose their last owner (EC-37). It does
// nothing unless owner is being replaced by a lower role or removed.
//
// The owner rows of the root are locked first, so two concurrent removals queue:
// the second one rereads the owners after the first committed and sees the
// real remaining count (EC-36). If the waiting actor was one of the owners
// removed meanwhile, the reread shows nothing and the request is refused the
// same way: the root keeps its owner.
func (d Deps) guardLastRootOwner(ctx context.Context, tx port.Tx, root bool, nodeID dnode.ID, target user.ID, old, next role.Role) error {
	if !root || old != role.Owner || next == role.Owner {
		return nil
	}
	owners, err := d.Members.LockActiveOwners(ctx, tx, nodeID)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if o != target {
			return nil
		}
	}
	return apperr.New(apperr.LastRootOwner, "the root node must keep at least one active owner")
}

func (d Deps) audit(ctx context.Context, tx port.Tx, e port.AuditEntry) error {
	return d.Audit.Write(ctx, tx, e)
}

func validation(field string, err error) error {
	detail := "invalid"
	if c, ok := apperr.CodeOf(err); ok {
		detail = string(c)
	}
	return apperr.New(apperr.ValidationFailed, "invalid "+field).WithField(field, detail)
}

func requireNodeID(id dnode.ID) error {
	if id.IsZero() {
		return apperr.New(apperr.ValidationFailed, "node_id is required").WithField("node_id", "required")
	}
	return nil
}

func requireUserID(id user.ID) error {
	if id.IsZero() {
		return apperr.New(apperr.ValidationFailed, "user_id is required").WithField("user_id", "required")
	}
	return nil
}

// Limits of list requests.
const maxLimit = 200

func encodeCursor(c port.MemberCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.Email + "\x00" + c.UserID.String() + "\x00" + c.NodeID.String()))
}

func decodeCursor(s string) (*port.MemberCursor, error) {
	if s == "" {
		return nil, nil
	}
	bad := apperr.New(apperr.ValidationFailed, "invalid cursor").WithField("cursor", "invalid")
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, bad
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 {
		return nil, bad
	}
	uid, err := uuid.Parse(parts[1])
	if err != nil {
		return nil, bad
	}
	nid, err := uuid.Parse(parts[2])
	if err != nil {
		return nil, bad
	}
	return &port.MemberCursor{Email: parts[0], UserID: user.ID(uid), NodeID: dnode.ID(nid)}, nil
}
