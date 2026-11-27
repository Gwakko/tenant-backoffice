package audit_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/audit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

var at = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func requireCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	require.Error(t, err)
	got, ok := apperr.CodeOf(err)
	require.True(t, ok, "not an apperr: %v", err)
	require.Equal(t, code, got)
}

func params() audit.Params {
	return audit.Params{
		ID:         audit.ID(uuid.New()),
		OccurredAt: at,
		ActorID:    user.ID(uuid.New()),
		NodeID:     node.ID(uuid.New()),
		Action:     audit.NodeRenamed,
		TargetID:   "n-1",
		Before:     audit.Fields{{Key: "name", Value: "Old"}},
		After:      audit.Fields{{Key: "name", Value: "New"}},
		RequestID:  "req-1",
	}
}

// The action list is closed; the switch in TargetType must cover each member.
func TestActions(t *testing.T) {
	all := audit.Actions()
	require.Len(t, all, 18)
	seen := map[audit.Action]bool{}
	for _, a := range all {
		require.False(t, seen[a], "duplicate %s", a)
		seen[a] = true
		require.True(t, a.Valid(), a)
		require.NotEmpty(t, a.TargetType(), a)
		parsed, err := audit.ParseAction(string(a))
		require.NoError(t, err)
		require.Equal(t, a, parsed)
	}
	for _, s := range []string{"", "node.exploded", "NODE.CREATED", "node.created "} {
		_, err := audit.ParseAction(s)
		requireCode(t, err, apperr.ValidationFailed)
		require.False(t, audit.Action(s).Valid())
	}
	require.Equal(t, audit.TargetBalance, audit.BalanceTransferred.TargetType())
	require.Equal(t, audit.TargetNode, audit.NodeChildMovedOut.TargetType())
	require.Equal(t, audit.TargetSetting, audit.SettingsUnset.TargetType())
}

func TestNewEntry(t *testing.T) {
	p := params()
	e, err := audit.NewEntry(p)
	require.NoError(t, err)
	require.Equal(t, p.ID, e.ID())
	require.Equal(t, at, e.OccurredAt())
	require.Equal(t, p.ActorID, e.ActorID())
	require.Equal(t, p.NodeID, e.NodeID())
	require.Equal(t, audit.NodeRenamed, e.Action())
	require.Equal(t, audit.TargetNode, e.TargetType()) // defaulted
	require.Equal(t, "n-1", e.TargetID())
	require.Equal(t, p.Before, e.Before())
	require.Equal(t, p.After, e.After())
	require.Equal(t, "req-1", e.RequestID())
	require.False(t, e.IsNoop())

	// EC-101: identical snapshots are a no-op.
	p.After = audit.Fields{{Key: "name", Value: "Old"}}
	e, err = audit.NewEntry(p)
	require.NoError(t, err)
	require.True(t, e.IsNoop())

	bad := map[string]func(*audit.Params){
		"action":   func(p *audit.Params) { p.Action = "nope" },
		"id":       func(p *audit.Params) { p.ID = audit.ID{} },
		"actor":    func(p *audit.Params) { p.ActorID = user.ID{} },
		"node":     func(p *audit.Params) { p.NodeID = node.ID{} },
		"time":     func(p *audit.Params) { p.OccurredAt = time.Time{} },
		"emptykey": func(p *audit.Params) { p.After = audit.Fields{{Key: "", Value: "x"}} },
	}
	for name, mutate := range bad {
		q := params()
		mutate(&q)
		_, err := audit.NewEntry(q)
		requireCode(t, err, apperr.ValidationFailed)
		_ = name
	}
}

// EC-100: secrets never enter a snapshot.
func TestNoSecrets(t *testing.T) {
	for _, k := range []string{"password", "Password", "password_hash", "new_password", "token", "refresh_token", "secret", "api_secret", "hash"} {
		p := params()
		p.After = audit.Fields{{Key: k, Value: "x"}}
		_, err := audit.NewEntry(p)
		requireCode(t, err, apperr.ValidationFailed)
		p = params()
		p.Before = audit.Fields{{Key: k, Value: "x"}}
		_, err = audit.NewEntry(p)
		requireCode(t, err, apperr.ValidationFailed)
	}
}

// An entry cannot be changed after creation: input and output slices are
// copies, and there is no setter or exported field.
func TestEntryImmutable(t *testing.T) {
	p := params()
	e, err := audit.NewEntry(p)
	require.NoError(t, err)

	// Changing the caller's slices afterwards does not reach the entry.
	p.Before[0].Value = "tampered"
	p.After[0].Value = "tampered"
	require.Equal(t, "Old", e.Before()[0].Value)
	require.Equal(t, "New", e.After()[0].Value)

	// Changing a returned slice does not reach the entry either.
	e.Before()[0].Value = "tampered"
	e.After()[0] = audit.Field{Key: "x", Value: "y"}
	require.Equal(t, "Old", e.Before()[0].Value)
	require.Equal(t, audit.Field{Key: "name", Value: "New"}, e.After()[0])

	// Structural guarantee: every struct field is unexported and no method
	// mutates the receiver (no Set*/Update*/With* style methods, pointer
	// receivers excluded altogether).
	typ := reflect.TypeOf(e)
	for i := 0; i < typ.NumField(); i++ {
		require.False(t, typ.Field(i).IsExported(), typ.Field(i).Name)
	}
	require.Zero(t, reflect.TypeOf(&e).NumMethod()-typ.NumMethod(), "pointer-receiver methods")
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		for _, prefix := range []string{"Set", "Update", "Change", "With", "Append", "Add"} {
			require.False(t, strings.HasPrefix(name, prefix), "mutator-like method %s", name)
		}
	}
}

func TestFieldsEqual(t *testing.T) {
	a := audit.Fields{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}
	require.True(t, a.Equal(audit.Fields{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}))
	require.False(t, a.Equal(audit.Fields{{Key: "b", Value: "2"}, {Key: "a", Value: "1"}}), "order matters")
	require.False(t, a.Equal(a[:1]))
	require.True(t, audit.Fields(nil).Equal(audit.Fields{}))
}
