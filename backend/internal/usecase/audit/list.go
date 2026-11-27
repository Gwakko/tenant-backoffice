// Package audit holds the read side of the audit journal (UC-AU1). Entries are
// written by the use cases that make the change, through port.AuditWriter, in
// the same transaction.
package audit

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	daudit "github.com/Gwakko/tenant-backoffice/backend/internal/domain/audit"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/authz"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

const maxLimit = 200

// Deps are the ports the audit use cases work through.
type Deps struct {
	Runner  port.TxRunner
	Checker authz.Checker
	Nodes   port.NodeRepo
	Audit   port.AuditRepo
}

// ListAuditInput is the request of UC-AU1.
type ListAuditInput struct {
	Actor       user.ActorID
	ScopeNodeID dnode.ID
	// Optional filters. NodeID must lie in the subtree of the scope, otherwise
	// the answer is not_found (EC-52).
	NodeID  *dnode.ID
	Action  *string
	ActorID *user.ID
	// From is inclusive, To exclusive.
	From, To *time.Time
	Limit    int
	Cursor   string
}

// Validate checks the input without touching the store.
func (in ListAuditInput) Validate() error {
	if in.ScopeNodeID.IsZero() {
		return apperr.New(apperr.ValidationFailed, "scope_node_id is required").WithField("scope_node_id", "required")
	}
	if in.Action != nil {
		if _, err := daudit.ParseAction(*in.Action); err != nil {
			return apperr.New(apperr.ValidationFailed, "unknown audit action").WithField("action", "unknown")
		}
	}
	if in.From != nil && in.To != nil && !in.From.Before(*in.To) {
		return apperr.New(apperr.ValidationFailed, "from must be before to").WithField("from", "after_to")
	}
	if in.Limit < 1 || in.Limit > maxLimit {
		return apperr.New(apperr.ValidationFailed, "limit must be 1..200").WithField("limit", "out_of_range")
	}
	_, err := decodeCursor(in.Cursor)
	return err
}

// ListAuditOutput is one page of the journal, newest first.
type ListAuditOutput struct {
	Items      []port.AuditRecord
	NextCursor string
}

// ListAudit is UC-AU1. It is read only.
type ListAudit struct{ d Deps }

// NewListAudit builds the use case.
func NewListAudit(d Deps) *ListAudit { return &ListAudit{d: d} }

// Execute requires audit.read on the scope node (a node the actor cannot see
// is not_found) and returns the entries of the scope's subtree that the
// actor's row-level visibility shows, ordered by (occurred_at, id) descending.
func (uc *ListAudit) Execute(ctx context.Context, in ListAuditInput) (ListAuditOutput, error) {
	if err := in.Validate(); err != nil {
		return ListAuditOutput{}, err
	}
	after, _ := decodeCursor(in.Cursor)
	f := port.AuditFilter{ScopeNodeID: in.ScopeNodeID, NodeID: in.NodeID, ActorID: in.ActorID, From: in.From, To: in.To}
	if in.Action != nil {
		a := daudit.Action(*in.Action)
		f.Action = &a
	}
	d := uc.d
	out := ListAuditOutput{Items: []port.AuditRecord{}}
	err := d.Runner.InTx(ctx, in.Actor, port.TxOptions{ReadOnly: true}, func(ctx context.Context, tx port.Tx) error {
		if _, err := authz.Require(ctx, d.Checker, tx, in.Actor, in.ScopeNodeID, role.AuditRead); err != nil {
			return err
		}
		if in.NodeID != nil {
			inside, err := d.Nodes.IsInSubtree(ctx, tx, *in.NodeID, in.ScopeNodeID)
			if err != nil {
				return err
			}
			if !inside {
				return apperr.NotFoundError("node")
			}
		}
		page, err := d.Audit.List(ctx, tx, f, after, in.Limit+1)
		if err != nil {
			return err
		}
		if len(page) > in.Limit {
			page = page[:in.Limit]
			last := page[len(page)-1].Entry
			out.NextCursor = encodeCursor(port.AuditCursor{OccurredAt: last.OccurredAt(), ID: last.ID()})
		}
		out.Items = append(out.Items, page...)
		return nil
	})
	if err != nil {
		return ListAuditOutput{}, err
	}
	return out, nil
}

// encodeCursor and decodeCursor turn the keyset position into an opaque
// string. The position carries no rights: what a page shows is decided by the
// actor's visibility on every request.
func encodeCursor(c port.AuditCursor) string {
	raw := strconv.FormatInt(c.OccurredAt.UnixMicro(), 10) + "\x00" + uuid.UUID(c.ID).String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(s string) (*port.AuditCursor, error) {
	if s == "" {
		return nil, nil
	}
	bad := apperr.New(apperr.ValidationFailed, "invalid cursor").WithField("cursor", "invalid")
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, bad
	}
	ts, id, ok := strings.Cut(string(raw), "\x00")
	if !ok {
		return nil, bad
	}
	micro, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, bad
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, bad
	}
	return &port.AuditCursor{OccurredAt: time.UnixMicro(micro).UTC(), ID: daudit.ID(u)}, nil
}
