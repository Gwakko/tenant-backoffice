//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/node"
)

const repeats = 20

// parallel runs the functions at the same moment and returns their errors.
func parallel(fns ...func() error) []error {
	errs := make([]error, len(fns))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = fn()
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

func (w *world) requireConsistent(msg string) {
	w.t.Helper()
	assert.Empty(w.t, w.violations(), msg)
}

func label(id node.ID) string { return strings.ReplaceAll(id.String(), "-", "") }

// Test-ID: T-I-04
func TestParallelMovesSameNode(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.addNode(ctx, "P0", node.TypePartner, "root")
	w.addNode(ctx, "P1", node.TypePartner, "root")
	w.addNode(ctx, "P2", node.TypePartner, "root")
	w.addNode(ctx, "C", node.TypeCompany, "P0")
	c := w.useCases()

	for i := 0; i < repeats; i++ {
		require.NoError(t, w.moveAs(c, "ownerRoot", "C", "P0"))
		c.audit.reset()
		errs := parallel(
			func() error { return w.moveAs(c, "ownerRoot", "C", "P1") },
			func() error { return w.moveAs(c, "ownerRoot", "C", "P2") },
		)
		require.NoError(t, errs[0], "iteration %d", i)
		require.NoError(t, errs[1], "iteration %d", i)
		w.requireConsistent(fmt.Sprint("iteration ", i))

		moved := c.audit.entriesOf("node.moved")
		require.Len(t, moved, 2, "two node.moved records, iteration %d", i)
		last := moved[1].After["parent_id"] // the commit that came last wins
		path, parent := w.pathOf(w.id("C"))
		require.NotNil(t, parent)
		assert.Equal(t, last, *parent, "iteration %d", i)
		parentPath, _ := w.pathOf(w.id("P1"))
		if last == w.id("P2").String() {
			parentPath, _ = w.pathOf(w.id("P2"))
		}
		assert.Equal(t, parentPath+"."+label(w.id("C")), path)
	}
}

// Test-ID: T-I-05
func TestMoveNodeAndAncestor(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.addNode(ctx, "A2", node.TypeCompany, "A")
	c := w.useCases()

	for i := 0; i < repeats; i++ {
		// Reset: A2 back under A, the unit back under A1.
		require.NoError(t, w.moveAs(c, "ownerRoot", "A2", "A"))
		require.NoError(t, w.moveAs(c, "ownerRoot", "A1u1", "A1"))

		errs := parallel(
			func() error { return w.moveAs(c, "ownerRoot", "A1u1", "A2") }, // the unit joins A2 ...
			func() error { return w.moveAs(c, "ownerRoot", "A2", "B") },    // ... while A2 leaves A
		)
		require.NoError(t, errs[0], "iteration %d", i)
		require.NoError(t, errs[1], "iteration %d", i)
		w.requireConsistent(fmt.Sprint("iteration ", i))

		a2Path, _ := w.pathOf(w.id("A2"))
		uPath, uParent := w.pathOf(w.id("A1u1"))
		assert.Equal(t, w.id("A2").String(), *uParent)
		assert.Equal(t, a2Path+"."+label(w.id("A1u1")), uPath, "the unit's path reflects both moves")
		bPath, _ := w.pathOf(w.id("B"))
		assert.True(t, strings.HasPrefix(uPath, bPath+"."), "and A2 now lives under B")
	}
}

// Test-ID: T-I-06
func TestCreateDuringParentMove(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.addNode(ctx, "P0", node.TypePartner, "root")
	w.addNode(ctx, "P1", node.TypePartner, "root")
	w.addNode(ctx, "C", node.TypeCompany, "P0")
	c := w.useCases()
	owner := w.users["ownerRoot"]

	for i := 0; i < repeats; i++ {
		require.NoError(t, w.moveAs(c, "ownerRoot", "C", "P0"))
		var created uc.CreateNodeOutput
		errs := parallel(
			func() error {
				var err error
				created, err = c.create.Execute(ctx, uc.CreateNodeInput{
					Actor: owner, ParentID: w.id("C"), Type: "unit", Name: fmt.Sprint("u", i),
				})
				return err
			},
			func() error { return w.moveAs(c, "ownerRoot", "C", "P1") },
		)
		require.NoError(t, errs[0], "iteration %d", i)
		require.NoError(t, errs[1], "iteration %d", i)
		w.requireConsistent(fmt.Sprint("iteration ", i))

		cPath, _ := w.pathOf(w.id("C"))
		uPath, _ := w.pathOf(created.Node.Node.ID)
		assert.Equal(t, cPath+"."+label(created.Node.Node.ID), uPath, "the new unit starts with the new path of C")
		assert.True(t, strings.HasPrefix(cPath, mustPath(w, "P1")+"."))
	}
}

func mustPath(w *world, key string) string {
	p, _ := w.pathOf(w.id(key))
	return p
}
