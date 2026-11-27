package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/audit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

const (
	resourceAudit = "audit entry"

	insertAudit = `INSERT INTO audit_log
  (id, occurred_at, actor_id, node_id, action, target_type, target_id, before, after, request_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10)`

	// Entries of nodes inside the subtree of $1 only. audit_log and nodes are
	// both filtered by row-level security, so a node the actor cannot see
	// drops its entries out. users may hide the actor of an entry: the e-mail
	// is then NULL. Keyset on (occurred_at DESC, id DESC).
	listAudit = `SELECT a.id, a.occurred_at, a.actor_id, a.node_id, a.action, a.target_type,
       a.target_id, a.before, a.after, a.request_id, coalesce(u.email, ''), n.name
  FROM audit_log a
  JOIN nodes n ON n.id = a.node_id
  LEFT JOIN users u ON u.id = a.actor_id
 WHERE n.path <@ (SELECT path FROM nodes WHERE id = $1)
   AND ($2::uuid IS NULL OR a.node_id = $2)
   AND ($3::text IS NULL OR a.action = $3)
   AND ($4::uuid IS NULL OR a.actor_id = $4)
   AND ($5::timestamptz IS NULL OR a.occurred_at >= $5)
   AND ($6::timestamptz IS NULL OR a.occurred_at < $6)
   AND ($7::timestamptz IS NULL OR (a.occurred_at, a.id) < ($7, $8::uuid))
 ORDER BY a.occurred_at DESC, a.id DESC
 LIMIT $9`
)

// AuditWriter implements port.AuditWriter. It writes through the transaction
// it is given, never through the pool, so the entry commits and rolls back
// with the change it describes (EC-96).
type AuditWriter struct {
	log   *slog.Logger
	clock port.Clock
	ids   port.IDGen
}

var _ port.AuditWriter = (*AuditWriter)(nil)

// NewAuditWriter builds the writer.
func NewAuditWriter(log *slog.Logger, clock port.Clock, ids port.IDGen) *AuditWriter {
	return &AuditWriter{log: log, clock: clock, ids: ids}
}

// Write implements port.AuditWriter. The entry goes through audit.NewEntry,
// which refuses unknown actions and secret-looking keys; a snapshot pair that
// does not differ is not recorded (EC-101).
func (w *AuditWriter) Write(ctx context.Context, tx port.Tx, e port.AuditEntry) error {
	t, err := unwrap(tx)
	if err != nil {
		return err
	}
	entry, err := audit.NewEntry(audit.Params{
		ID:         audit.ID(w.ids.New()),
		OccurredAt: w.clock.Now().UTC(),
		ActorID:    e.ActorID,
		NodeID:     e.NodeID,
		Action:     audit.Action(e.Action),
		TargetID:   e.TargetID.String(),
		Before:     fieldsOf(e.Before),
		After:      fieldsOf(e.After),
		RequestID:  e.RequestID,
	})
	if err != nil {
		return err
	}
	if e.TargetID == uuid.Nil {
		return apperr.New(apperr.ValidationFailed, "audit entry needs a target")
	}
	if entry.IsNoop() {
		return nil
	}
	before, err := marshalFields(entry.Before())
	if err != nil {
		return err
	}
	after, err := marshalFields(entry.After())
	if err != nil {
		return err
	}
	_, err = t.Exec(ctx, insertAudit,
		uuid.UUID(entry.ID()), entry.OccurredAt(), uuid.UUID(entry.ActorID()), uuid.UUID(entry.NodeID()),
		string(entry.Action()), string(entry.TargetType()), e.TargetID, before, after, entry.RequestID())
	if err != nil {
		return mapWriteError(w.log, "write audit", resourceAudit, err)
	}
	return nil
}

// fieldsOf orders a map by key, so equal snapshots compare equal.
func fieldsOf(m map[string]string) audit.Fields {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(audit.Fields, len(keys))
	for i, k := range keys {
		out[i] = audit.Field{Key: k, Value: m[k]}
	}
	return out
}

// marshalFields renders a snapshot as a jsonb object; no fields give SQL NULL.
func marshalFields(f audit.Fields) ([]byte, error) {
	if len(f) == 0 {
		return nil, nil
	}
	m := make(map[string]string, len(f))
	for _, x := range f {
		m[x.Key] = x.Value
	}
	return json.Marshal(m)
}

// AuditRepo implements port.AuditRepo.
type AuditRepo struct{ log *slog.Logger }

var _ port.AuditRepo = (*AuditRepo)(nil)

// NewAuditRepo builds the repository.
func NewAuditRepo(log *slog.Logger) *AuditRepo { return &AuditRepo{log: log} }

// List implements port.AuditRepo.
func (r *AuditRepo) List(ctx context.Context, tx port.Tx, f port.AuditFilter, after *port.AuditCursor, limit int) ([]port.AuditRecord, error) {
	t, err := unwrap(tx)
	if err != nil {
		return nil, err
	}
	var nodeID, actorID *uuid.UUID
	if f.NodeID != nil {
		v := uuid.UUID(*f.NodeID)
		nodeID = &v
	}
	if f.ActorID != nil {
		v := uuid.UUID(*f.ActorID)
		actorID = &v
	}
	var action *string
	if f.Action != nil {
		v := string(*f.Action)
		action = &v
	}
	var curAt *time.Time
	var curID uuid.UUID
	if after != nil {
		curAt, curID = &after.OccurredAt, uuid.UUID(after.ID)
	}
	rows, err := t.Query(ctx, listAudit, uuid.UUID(f.ScopeNodeID), nodeID, action, actorID, f.From, f.To, curAt, curID, limit)
	if err != nil {
		return nil, mapError(r.log, "list audit", resourceAudit, err)
	}
	defer rows.Close()
	var out []port.AuditRecord
	for rows.Next() {
		var (
			id, actor, nid, target uuid.UUID
			at                     time.Time
			act, tt, req           string
			before, afterJSON      []byte
			rec                    port.AuditRecord
		)
		if err := rows.Scan(&id, &at, &actor, &nid, &act, &tt, &target, &before, &afterJSON, &req, &rec.ActorEmail, &rec.NodeName); err != nil {
			return nil, mapError(r.log, "scan audit", resourceAudit, err)
		}
		bf, err := parseFields(before)
		if err != nil {
			return nil, fmt.Errorf("postgres: audit entry %s: %w", id, err)
		}
		af, err := parseFields(afterJSON)
		if err != nil {
			return nil, fmt.Errorf("postgres: audit entry %s: %w", id, err)
		}
		rec.Entry, err = audit.NewEntry(audit.Params{
			ID: audit.ID(id), OccurredAt: at.UTC(), ActorID: user.ID(actor), NodeID: node.ID(nid),
			Action: audit.Action(act), TargetType: audit.TargetType(tt), TargetID: target.String(),
			Before: bf, After: af, RequestID: req,
		})
		if err != nil {
			return nil, fmt.Errorf("postgres: audit entry %s: %w", id, err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(r.log, "list audit", resourceAudit, err)
	}
	return out, nil
}

func parseFields(raw []byte) (audit.Fields, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return fieldsOf(m), nil
}
