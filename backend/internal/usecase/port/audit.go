package port

import (
	"context"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// AuditEntry is one audit record: what happened to which node, by whom.
// Before and After hold only the fields that changed.
type AuditEntry struct {
	Action    string
	NodeID    node.ID
	ActorID   user.ActorID
	RequestID string
	Before    map[string]string
	After     map[string]string
}

// AuditWriter appends an entry in the transaction of the change it describes.
type AuditWriter interface {
	Write(ctx context.Context, tx Tx, e AuditEntry) error
}

// NopAudit is the AuditWriter used until the audit table exists (M3.3).
type NopAudit struct{}

var _ AuditWriter = NopAudit{}

// Write discards the entry.
func (NopAudit) Write(context.Context, Tx, AuditEntry) error { return nil }
