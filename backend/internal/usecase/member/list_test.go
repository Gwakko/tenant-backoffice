package member_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	uc "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/member"
)

func (w *world) list(actor, node string, inherited bool, limit int, cursor string) (uc.ListMembersOutput, error) {
	return uc.NewListMembers(w.deps).Execute(context.Background(), uc.ListMembersInput{
		Actor: w.ids[actor], NodeID: w.id(node), Inherited: inherited, Limit: limit, Cursor: cursor,
	})
}

func TestListMembers(t *testing.T) {
	w := newWorld(t)
	out, err := w.list("analystA", "A", false, 50, "")
	require.NoError(t, err)
	var emails []string
	for _, it := range out.Items {
		emails = append(emails, it.Email.String())
		assert.Nil(t, it.InheritedFrom)
		assert.Equal(t, w.id("A"), it.NodeID)
	}
	assert.Equal(t, []string{"admina@example.test", "analysta@example.test", "ownera@example.test", "viewera@example.test"}, emails)
	assert.Empty(t, out.NextCursor)
}

func TestListMembersInherited(t *testing.T) {
	w := newWorld(t)
	out, err := w.list("adminA", "A1", true, 50, "")
	require.NoError(t, err)
	fromA, fromRoot := 0, 0
	for _, it := range out.Items {
		switch {
		case it.InheritedFrom == nil:
		case *it.InheritedFrom == w.id("A"):
			fromA++
		case *it.InheritedFrom == w.id("root"):
			fromRoot++
		}
	}
	assert.Equal(t, 4, fromA)
	assert.Equal(t, 1, fromRoot)
	assert.Len(t, out.Items, 6, "four from A, one from the root, one on A1")

	direct, err := w.list("adminA", "A1", false, 50, "")
	require.NoError(t, err)
	assert.Len(t, direct.Items, 1)
}

func TestListMembersPaging(t *testing.T) {
	w := newWorld(t)
	first, err := w.list("adminA", "A", false, 3, "")
	require.NoError(t, err)
	require.Len(t, first.Items, 3)
	require.NotEmpty(t, first.NextCursor)
	second, err := w.list("adminA", "A", false, 3, first.NextCursor)
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	assert.Empty(t, second.NextCursor)
}

func TestListMembersAuthorization(t *testing.T) {
	w := newWorld(t)
	_, err := w.list("viewerA", "A", false, 10, "")
	requireCode(t, err, apperr.Forbidden)
	_, err = w.list("adminB", "A", false, 10, "")
	requireCode(t, err, apperr.NotFound)
	_, err = w.list("adminA", "A", false, 0, "")
	requireCode(t, err, apperr.ValidationFailed)
	_, err = w.list("adminA", "A", false, 10, "not-a-cursor")
	requireCode(t, err, apperr.ValidationFailed)
}
