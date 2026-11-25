// Package node holds the use cases of tree nodes (UC-N1..N11). Every use case
// checks the actor's rights on every call against the roles stored in the
// database: an object outside the actor's subtrees is not_found, indistinguishable
// from a missing one; an object inside them without the right is forbidden.
package node

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/authz"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// Deps are the ports every node use case works through.
type Deps struct {
	Runner  port.TxRunner
	Checker authz.Checker
	Nodes   port.NodeRepo
	Lock    port.TreeLock
	Audit   port.AuditWriter
	Clock   port.Clock
	IDs     port.IDGen
}

// Crumb is one element of the path shown above a node.
type Crumb struct {
	ID   dnode.ID
	Name string
	Type dnode.Type
}

// View is a node as shown to a client: the entity, its effective activity, the
// number of direct children and the path from the actor's access root.
type View struct {
	Node            dnode.Node
	EffectiveActive bool
	ChildrenCount   int
	Breadcrumbs     []Crumb
}

// Summary is a node in a list.
type Summary struct {
	ID              dnode.ID
	Type            dnode.Type
	Name            string
	Status          dnode.Status
	EffectiveActive bool
}

func summarize(n dnode.Node) Summary {
	return Summary{ID: n.ID, Type: n.Type, Name: n.Name.String(), Status: n.Status, EffectiveActive: n.EffectiveActive()}
}

var (
	readTx  = port.TxOptions{ReadOnly: true}
	writeTx = port.TxOptions{}
)

// view builds the View of n inside tx.
func (d Deps) view(ctx context.Context, tx port.Tx, n dnode.Node) (View, error) {
	children, err := d.Nodes.CountChildren(ctx, tx, n.ID)
	if err != nil {
		return View{}, err
	}
	chain, err := d.Nodes.Ancestors(ctx, tx, n.ID)
	if err != nil {
		return View{}, err
	}
	crumbs := make([]Crumb, len(chain))
	for i, a := range chain {
		crumbs[i] = Crumb{ID: a.ID, Name: a.Name.String(), Type: a.Type}
	}
	return View{Node: n, EffectiveActive: n.EffectiveActive(), ChildrenCount: children, Breadcrumbs: crumbs}, nil
}

// require checks perm on the node itself.
func (d Deps) require(ctx context.Context, tx port.Tx, actor user.ActorID, id dnode.ID, perm role.Permission) error {
	_, err := authz.Require(ctx, d.Checker, tx, actor, id, perm)
	return err
}

// requireOnParent checks perm on the parent of a node the actor can already
// see. The parent exists, so a parent the actor cannot see (the node is the
// actor's access root) means the right is missing: forbidden, not not_found.
func (d Deps) requireOnParent(ctx context.Context, tx port.Tx, actor user.ActorID, parent dnode.ID, perm role.Permission) error {
	err := d.require(ctx, tx, actor, parent, perm)
	if errors.Is(err, apperr.New(apperr.NotFound, "")) {
		return apperr.ForbiddenError("the role does not allow this action")
	}
	return err
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

func requireID(field string, id dnode.ID) error {
	if id.IsZero() {
		return apperr.New(apperr.ValidationFailed, field+" is required").WithField(field, "required")
	}
	return nil
}

// Limits of list requests.
const (
	maxLimit    = 200
	maxQueryLen = 100
)

func validateList(limit int, query string) error {
	if limit < 1 || limit > maxLimit {
		return apperr.New(apperr.ValidationFailed, "limit must be 1..200").WithField("limit", "out_of_range")
	}
	if len([]rune(query)) > maxQueryLen {
		return apperr.New(apperr.ValidationFailed, "query is too long").WithField("query", "too_long")
	}
	return nil
}

// searchKey prepares a user query for the name-key match.
func searchKey(q string) string { return strings.ToLower(strings.TrimSpace(q)) }

// encodeCursor and decodeCursor turn the keyset position into an opaque string.
// The position carries no rights: what a page shows is still decided by the
// actor's visibility.
func encodeCursor(c port.ChildCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.NameKey + "\x00" + c.ID.String()))
}

func decodeCursor(s string) (*port.ChildCursor, error) {
	if s == "" {
		return nil, nil
	}
	bad := apperr.New(apperr.ValidationFailed, "invalid cursor").WithField("cursor", "invalid")
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, bad
	}
	key, id, ok := strings.Cut(string(raw), "\x00")
	if !ok {
		return nil, bad
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, bad
	}
	return &port.ChildCursor{NameKey: key, ID: dnode.ID(u)}, nil
}
