package node_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
)

// Test-ID: T-U-03
func TestPlanMoveCycle(t *testing.T) {
	tr := newTree(t)

	// EC-05: into itself.
	_, err := tr.partner.PlanMove(tr.partner)
	requireCode(t, err, apperr.MoveCycle)

	// EC-04: into own descendant (type check would also fail; cycle comes first).
	_, err = tr.partner.PlanMove(tr.company)
	requireCode(t, err, apperr.MoveCycle)
	_, err = tr.partner.PlanMove(tr.unit)
	requireCode(t, err, apperr.MoveCycle)

	// Cycle is reported before an inactive-parent error.
	inactive, _ := tr.company.Deactivate(testNow)
	_, err = tr.partner.PlanMove(inactive)
	requireCode(t, err, apperr.MoveCycle)
}

// Test-ID: T-U-04
func TestPlanMoveWrongType(t *testing.T) {
	tr := newTree(t)
	other := build(t, &tr.platform, node.TypePartner, "Other")
	otherCompany := build(t, &other, node.TypeCompany, "Other company")

	// EC-06: company under company, and under platform.
	_, err := tr.company.PlanMove(otherCompany)
	requireCode(t, err, apperr.InvalidParentType)
	_, err = tr.company.PlanMove(tr.platform)
	requireCode(t, err, apperr.InvalidParentType)

	// Type error is reported before an inactive-parent error.
	inactive, _ := tr.platform.Deactivate(testNow)
	_, err = tr.company.PlanMove(inactive)
	requireCode(t, err, apperr.InvalidParentType)

	// A valid move yields the new path.
	plan, err := tr.company.PlanMove(other)
	require.NoError(t, err)
	require.False(t, plan.NoOp)
	require.Equal(t, node.Path{tr.platform.ID, other.ID, tr.company.ID}, plan.NewPath)
	require.Equal(t, tr.company.Path, plan.OldPath)
	require.Equal(t, other.ID, plan.NewParentID)
}

// Test-ID: T-U-06
func TestRootImmutable(t *testing.T) {
	tr := newTree(t)
	_, err := tr.platform.PlanMove(tr.partner)
	requireCode(t, err, apperr.RootImmutable)
	_, err = tr.platform.PlanMove(tr.platform)
	requireCode(t, err, apperr.RootImmutable) // root check precedes cycle check
	requireCode(t, tr.platform.CanDelete(node.DeleteStats{}), apperr.RootImmutable)
}

// Test-ID: T-U-07
func TestMoveUnderInactive(t *testing.T) {
	tr := newTree(t)
	other := build(t, &tr.platform, node.TypePartner, "Other")
	otherCompany := build(t, &other, node.TypeCompany, "Other company")

	// New parent itself inactive.
	inactive, _ := otherCompany.Deactivate(testNow)
	_, err := tr.unit.PlanMove(inactive)
	requireCode(t, err, apperr.NodeInactive)

	// New parent active, but an ancestor is inactive (EC-13).
	otherInactive, _ := other.Deactivate(testNow)
	underInactive := build(t, &otherInactive, node.TypeCompany, "Child of inactive")
	require.False(t, underInactive.EffectiveActive())
	_, err = tr.unit.PlanMove(underInactive)
	requireCode(t, err, apperr.NodeInactive)
}

// EC-07: moving under the current parent is a no-op (the use case test T-U-05
// lives with the use case).
func TestPlanMoveSameParentIsNoop(t *testing.T) {
	tr := newTree(t)
	plan, err := tr.unit.PlanMove(tr.company)
	require.NoError(t, err)
	require.True(t, plan.NoOp)
	require.Equal(t, tr.unit.Path, plan.NewPath)
}
