package audit

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// ID identifies an audit entry.
type ID uuid.UUID

// Field is one key/value pair of a before/after snapshot.
type Field struct{ Key, Value string }

// Fields is an ordered list of fields; the order is kept as given.
type Fields []Field

// secretMarkers are key fragments that must never appear in an audit snapshot.
var secretMarkers = []string{"password", "hash", "token", "secret"}

// Equal reports whether both lists have the same fields in the same order.
func (f Fields) Equal(o Fields) bool {
	if len(f) != len(o) {
		return false
	}
	for i := range f {
		if f[i] != o[i] {
			return false
		}
	}
	return true
}

func (f Fields) clone() Fields {
	if f == nil {
		return nil
	}
	out := make(Fields, len(f))
	copy(out, f)
	return out
}

func (f Fields) validate() error {
	for _, x := range f {
		if x.Key == "" {
			return apperr.New(apperr.ValidationFailed, "audit field key must not be empty")
		}
		k := strings.ToLower(x.Key)
		for _, m := range secretMarkers {
			if strings.Contains(k, m) {
				return apperr.New(apperr.ValidationFailed, "audit fields must not carry secrets")
			}
		}
	}
	return nil
}

// Params are the inputs of NewEntry.
type Params struct {
	ID         ID
	OccurredAt time.Time
	ActorID    user.ActorID
	NodeID     node.ID
	Action     Action
	TargetType TargetType
	TargetID   string
	Before     Fields
	After      Fields
	RequestID  string
}

// Entry is an immutable audit record: its fields are unexported, there are no
// setters, and the field lists are copied on the way in and on the way out.
type Entry struct {
	id         ID
	occurredAt time.Time
	actorID    user.ActorID
	nodeID     node.ID
	action     Action
	targetType TargetType
	targetID   string
	before     Fields
	after      Fields
	requestID  string
}

// NewEntry validates p and builds an Entry. The action must be known, the
// time, actor, node and entry IDs set, and the snapshots free of secrets.
// TargetType defaults to the action's own target type.
func NewEntry(p Params) (Entry, error) {
	if !p.Action.Valid() {
		return Entry{}, apperr.New(apperr.ValidationFailed, "unknown audit action")
	}
	if p.ID == (ID{}) || p.ActorID == (user.ID{}) || p.NodeID.IsZero() || p.OccurredAt.IsZero() {
		return Entry{}, apperr.New(apperr.ValidationFailed, "audit entry needs id, actor, node and time")
	}
	if err := p.Before.validate(); err != nil {
		return Entry{}, err
	}
	if err := p.After.validate(); err != nil {
		return Entry{}, err
	}
	tt := p.TargetType
	if tt == "" {
		tt = p.Action.TargetType()
	}
	return Entry{
		id: p.ID, occurredAt: p.OccurredAt, actorID: p.ActorID, nodeID: p.NodeID,
		action: p.Action, targetType: tt, targetID: p.TargetID,
		before: p.Before.clone(), after: p.After.clone(), requestID: p.RequestID,
	}, nil
}

// ID returns the entry id.
func (e Entry) ID() ID { return e.id }

// OccurredAt returns the time of the change.
func (e Entry) OccurredAt() time.Time { return e.occurredAt }

// ActorID returns who made the change.
func (e Entry) ActorID() user.ActorID { return e.actorID }

// NodeID returns the node the entry is recorded on.
func (e Entry) NodeID() node.ID { return e.nodeID }

// Action returns what happened.
func (e Entry) Action() Action { return e.action }

// TargetType returns the kind of the changed object.
func (e Entry) TargetType() TargetType { return e.targetType }

// TargetID returns the identifier of the changed object.
func (e Entry) TargetID() string { return e.targetID }

// Before returns a copy of the snapshot before the change.
func (e Entry) Before() Fields { return e.before.clone() }

// After returns a copy of the snapshot after the change.
func (e Entry) After() Fields { return e.after.clone() }

// RequestID returns the request correlation id.
func (e Entry) RequestID() string { return e.requestID }

// IsNoop reports whether the change left the snapshots identical; such changes
// are not recorded.
func (e Entry) IsNoop() bool { return e.before.Equal(e.after) }
