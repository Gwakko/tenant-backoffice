package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/authz"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

type fakeTx struct{}

func (fakeTx) TxMarker() {}

// fakeChecker answers from a map; a node absent from the map has no role.
type fakeChecker struct {
	roles map[node.ID]role.Role
	err   error
	calls int
}

func (f *fakeChecker) EffectiveRole(_ context.Context, _ port.Tx, _ user.ActorID, id node.ID) (role.Role, bool, error) {
	f.calls++
	if f.err != nil {
		return "", false, f.err
	}
	r, ok := f.roles[id]
	return r, ok, nil
}

// Test-ID: T-U-40
func TestRequireNotFoundVersusForbidden(t *testing.T) {
	visible := node.ID(uuid.New())
	hidden := node.ID(uuid.New())
	missing := node.ID(uuid.New())
	actor := user.ActorID(uuid.New())
	c := &fakeChecker{roles: map[node.ID]role.Role{visible: role.Viewer}}

	t.Run("no role: hidden and missing nodes give the identical not_found", func(t *testing.T) {
		_, errHidden := authz.Require(t.Context(), c, fakeTx{}, actor, hidden, role.NodeRead)
		_, errMissing := authz.Require(t.Context(), c, fakeTx{}, actor, missing, role.NodeRead)
		require.Equal(t, apperr.NotFoundError("node"), errHidden)
		require.Equal(t, errHidden, errMissing, "code and message must not depend on why there is no role")
	})

	t.Run("role without the permission: forbidden, with the role", func(t *testing.T) {
		r, err := authz.Require(t.Context(), c, fakeTx{}, actor, visible, role.NodeRename)
		require.ErrorIs(t, err, apperr.New(apperr.Forbidden, ""))
		require.Equal(t, role.Viewer, r)
	})

	t.Run("role with the permission", func(t *testing.T) {
		r, err := authz.Require(t.Context(), c, fakeTx{}, actor, visible, role.NodeRead)
		require.NoError(t, err)
		require.Equal(t, role.Viewer, r)
	})

	t.Run("every role against every permission follows the matrix", func(t *testing.T) {
		for _, rl := range role.All() {
			id := node.ID(uuid.New())
			cc := &fakeChecker{roles: map[node.ID]role.Role{id: rl}}
			for _, p := range role.AllPermissions() {
				_, err := authz.Require(t.Context(), cc, fakeTx{}, actor, id, p)
				if role.Allows(rl, p) {
					require.NoError(t, err, "%s %s", rl, p)
				} else {
					require.ErrorIs(t, err, apperr.New(apperr.Forbidden, ""), "%s %s", rl, p)
				}
			}
		}
	})

	t.Run("checker failure is passed through, not turned into not_found", func(t *testing.T) {
		boom := errors.New("boom")
		_, err := authz.Require(t.Context(), &fakeChecker{err: boom}, fakeTx{}, actor, visible, role.NodeRead)
		require.ErrorIs(t, err, boom)
	})
}
