package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/audit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// AuditEntry is one audit record to write: what happened to which object, on
// which node, by whom. Before and After hold only the fields that changed and
// never a secret (the writer refuses such keys).
type AuditEntry struct {
	Action string
	// NodeID is the node the entry is recorded on (and shown under).
	NodeID node.ID
	// TargetID identifies the changed object: the node, the user or the
	// moved child.
	TargetID  uuid.UUID
	ActorID   user.ActorID
	RequestID string
	Before    map[string]string
	After     map[string]string
}

// AuditWriter appends an entry in the transaction of the change it describes:
// when that transaction rolls back, so does the entry (EC-96).
type AuditWriter interface {
	Write(ctx context.Context, tx Tx, e AuditEntry) error
}

// AuditFilter selects the entries of UC-AU1. Only entries on nodes inside the
// subtree of ScopeNodeID are returned, and only those the actor can see.
type AuditFilter struct {
	ScopeNodeID node.ID
	NodeID      *node.ID
	Action      *audit.Action
	ActorID     *user.ID
	// From is inclusive, To exclusive.
	From, To *time.Time
}

// AuditCursor is the keyset position after which the next page starts. The
// order is (occurred_at DESC, id DESC).
type AuditCursor struct {
	OccurredAt time.Time
	ID         audit.ID
}

// AuditRecord is an entry with the display data of the actor and the node.
// ActorEmail is empty when the actor is not visible to the reader.
type AuditRecord struct {
	Entry      audit.Entry
	ActorEmail string
	NodeName   string
}

// AuditRepo reads the journal.
type AuditRepo interface {
	List(ctx context.Context, tx Tx, f AuditFilter, after *AuditCursor, limit int) ([]AuditRecord, error)
}
