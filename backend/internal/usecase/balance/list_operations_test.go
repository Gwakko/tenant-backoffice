package balance_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	dbalance "github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	dnode "github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/balance"
)

func (w *world) list(who, scope string, mod func(*uc.ListOperationsInput)) (uc.ListOperationsOutput, error) {
	in := uc.ListOperationsInput{Actor: w.actors[who], ScopeNodeID: w.id(scope), Limit: 2}
	if mod != nil {
		mod(&in)
	}
	return uc.NewListOperations(w.deps).Execute(context.Background(), in)
}

func TestListOperationsKeysetPagesWithEqualTimestamps(t *testing.T) {
	w := newWorld(t)
	for i := range 5 { // the fake clock does not move: every created_at is equal
		_, err := w.credit("adminA", "u1", "10", fmt.Sprintf("k%d", i))
		require.NoError(t, err)
	}
	var seen []dbalance.OperationID
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, 5, "a page must make progress")
		out, err := w.list("adminA", "A", func(in *uc.ListOperationsInput) { in.Cursor = cursor })
		require.NoError(t, err)
		for _, it := range out.Items {
			seen = append(seen, it.Operation.ID)
			assert.Equal(t, "u1", it.NodeName)
		}
		if out.NextCursor == "" {
			break
		}
		cursor = out.NextCursor
	}
	require.Len(t, seen, 5, "no repeats and no gaps")
	uniq := map[dbalance.OperationID]bool{}
	for _, id := range seen {
		uniq[id] = true
	}
	assert.Len(t, uniq, 5)
	for i := 1; i < len(seen); i++ {
		assert.Greater(t, string(seen[i-1][:]), string(seen[i][:]), "id descending on equal created_at")
	}
}

func TestListOperationsFiltersAndScope(t *testing.T) {
	w := newWorld(t)
	w.fund("u1", 1000)
	_, err := w.credit("adminA", "u1", "10", "k1")
	require.NoError(t, err)
	_, err = w.debit("adminA", "u1", "5", "k2")
	require.NoError(t, err)
	_, err = w.credit("adminB", "B1", "7", "k3")
	require.NoError(t, err)

	out, err := w.list("adminA", "A", func(in *uc.ListOperationsInput) { in.Limit = 50 })
	require.NoError(t, err)
	assert.Len(t, out.Items, 2)

	out, err = w.list("adminA", "A", func(in *uc.ListOperationsInput) { in.Limit = 50; in.Kinds = []string{"debit", "debit"} })
	require.NoError(t, err)
	require.Len(t, out.Items, 1)
	assert.Equal(t, dbalance.Debit, out.Items[0].Operation.Kind)

	// The owner sees both branches from the root; a node filter narrows it.
	out, err = w.list("owner", "root", func(in *uc.ListOperationsInput) { in.Limit = 50 })
	require.NoError(t, err)
	assert.Len(t, out.Items, 3)
	b1 := w.id("B1")
	out, err = w.list("owner", "root", func(in *uc.ListOperationsInput) { in.Limit = 50; in.NodeID = &b1 })
	require.NoError(t, err)
	assert.Len(t, out.Items, 1)

	// A viewer has operation.read.
	_, err = w.list("viewerA", "A", nil)
	require.NoError(t, err)

	// EC-53: a foreign scope, a node outside the scope and an unknown node are
	// all not_found.
	_, err = w.list("adminA", "B", nil)
	requireCode(t, err, apperr.NotFound)
	_, err = w.list("nobody", "A", nil)
	requireCode(t, err, apperr.NotFound)
	_, err = w.list("adminA", "A", func(in *uc.ListOperationsInput) { in.NodeID = &b1 })
	requireCode(t, err, apperr.NotFound)
	_, err = w.list("adminA", "A", func(in *uc.ListOperationsInput) { id := dnode.ID{1}; in.NodeID = &id })
	requireCode(t, err, apperr.NotFound)
}

func TestListOperationsValidation(t *testing.T) {
	w := newWorld(t)
	now := time.Now()
	for name, mod := range map[string]func(*uc.ListOperationsInput){
		"limit 0":    func(in *uc.ListOperationsInput) { in.Limit = 0 },
		"limit 201":  func(in *uc.ListOperationsInput) { in.Limit = 201 },
		"bad kind":   func(in *uc.ListOperationsInput) { in.Kinds = []string{"credit') OR ('1'='1"} },
		"from to":    func(in *uc.ListOperationsInput) { in.From, in.To = &now, &now },
		"no scope":   func(in *uc.ListOperationsInput) { in.ScopeNodeID = dnode.ID{} },
		"zero nodes": func(in *uc.ListOperationsInput) { in.NodeID = &dnode.ID{} },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := w.list("adminA", "A", mod)
			requireCode(t, err, apperr.ValidationFailed)
		})
	}
	_, err := w.list("adminA", "A", func(in *uc.ListOperationsInput) { in.Cursor = "garbage" })
	requireCode(t, err, apperr.InvalidCursor)
}

func TestGetOperation(t *testing.T) {
	w := newWorld(t)
	out, err := w.credit("adminA", "u1", "10", "k1")
	require.NoError(t, err)
	get := func(who string) (uc.GetOperationOutput, error) {
		return uc.NewGetOperation(w.deps).Execute(context.Background(),
			uc.GetOperationInput{Actor: w.actors[who], OperationID: out.Operation.ID})
	}
	for _, who := range []string{"adminA", "viewerA", "owner"} {
		got, err := get(who)
		require.NoError(t, err, who)
		assert.Equal(t, out.Operation, got.Operation)
	}
	// EC-53: another branch and a stranger get not_found, never forbidden.
	for _, who := range []string{"adminB", "nobody"} {
		_, err := get(who)
		requireCode(t, err, apperr.NotFound)
	}
	_, err = uc.NewGetOperation(w.deps).Execute(context.Background(),
		uc.GetOperationInput{Actor: w.actors["adminA"], OperationID: dbalance.OperationID{9}})
	requireCode(t, err, apperr.NotFound)
	_, err = uc.NewGetOperation(w.deps).Execute(context.Background(), uc.GetOperationInput{Actor: w.actors["adminA"]})
	requireCode(t, err, apperr.ValidationFailed)
}
