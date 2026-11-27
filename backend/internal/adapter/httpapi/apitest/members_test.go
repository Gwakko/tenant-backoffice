//go:build integration

package apitest_test

import (
	"encoding/json"
	"net/http"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

const newPassword12 = "long-enough-password"

func keysOf(t *testing.T, body []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &m))
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Test-ID: T-A-07
// EC-42: an admin removes their own assignment; the node is not visible afterwards.
func TestAPISelfRevoke(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)

	requireStatus(t, admin.Get(nodeURL(w.A1, "")), http.StatusOK)
	requireStatus(t, admin.Delete(nodeURL(w.A, "/members/"+w.AdminA.String())), http.StatusNoContent)
	admin.Get(nodeURL(w.A, "")).RequireProblem(http.StatusNotFound, "not_found")
	admin.Get(nodeURL(w.A1, "")).RequireProblem(http.StatusNotFound, "not_found")
	require.Equal(t, []uuid.UUID{w.AdminA}, auditActors(t, s, "member.revoked", w.A))
}

// Proposed ID (not in spec 08 yet): T-A-40
func TestAPIMembers(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	path := nodeURL(w.A1, "/members")

	// A new email creates the user (with the initial password); an existing one is only assigned.
	created := admin.Post(path, map[string]any{"email": "Newbie@Example.test", "role": "viewer", "initialPassword": newPassword12})
	requireStatus(t, created, http.StatusCreated)
	m := decode[gen.Member](t, created)
	require.Equal(t, "newbie@example.test", string(m.Email))
	require.Equal(t, gen.Role("viewer"), m.Role)
	require.Equal(t, w.A1, m.NodeId)
	require.Nil(t, m.InheritedFrom)
	existing := admin.Post(path, map[string]any{"email": w.Emails[w.OnlyB], "role": "analyst", "initialPassword": "ignored-password-1"})
	requireStatus(t, existing, http.StatusCreated)
	require.Equal(t, keysOf(t, created.Body), keysOf(t, existing.Body), "the answer does not tell a created user from an existing one")

	// The created user can sign in with the initial password and sees only A1.
	newbie := signIn(t, s, "newbie@example.test", newPassword12)
	requireStatus(t, newbie.Get(nodeURL(w.A1, "")), http.StatusOK)
	newbie.Get(nodeURL(w.A, "")).RequireProblem(http.StatusNotFound, "not_found")

	// Refusals.
	p := admin.Post(path, map[string]any{"email": "nopass@example.test", "role": "viewer"}).
		RequireProblem(http.StatusBadRequest, "validation_failed")
	require.Equal(t, "initialPassword", p.Errors[0].Field)
	admin.Post(path, map[string]any{"email": "owner2@example.test", "role": "owner", "initialPassword": newPassword12}).
		RequireProblem(http.StatusForbidden, "role_not_grantable")
	admin.Post(path, map[string]any{"email": w.Emails[w.Inactive], "role": "viewer"}).
		RequireProblem(http.StatusConflict, "user_inactive")
	admin.Post(path, map[string]any{"email": "not-an-email", "role": "viewer"}).
		RequireProblem(http.StatusBadRequest, "validation_failed")
	admin.Post(path, map[string]any{"email": "x@example.test", "role": "emperor"}).
		RequireProblem(http.StatusBadRequest, "validation_failed")
	require.Zero(t, count(t, s, `SELECT count(*) FROM users WHERE email IN ('nopass@example.test','owner2@example.test')`))

	// Listing: own assignments, then the inherited ones too.
	own := decode[gen.MemberPage](t, admin.Get(path))
	emails := map[string]bool{}
	for _, it := range own.Items {
		emails[string(it.Email)] = true
		require.Nil(t, it.InheritedFrom)
	}
	require.True(t, emails["newbie@example.test"] && emails[w.Emails[w.OnlyB]] && emails[w.Emails[w.Multi]])
	require.False(t, emails[w.Emails[w.AdminA]], "AdminA holds the role on A, not on A1")
	all := decode[gen.MemberPage](t, admin.Get(path+"?inherited=true"))
	var fromA bool
	for _, it := range all.Items {
		if string(it.Email) == w.Emails[w.AdminA] {
			require.NotNil(t, it.InheritedFrom)
			require.Equal(t, w.A, *it.InheritedFrom)
			fromA = true
		}
	}
	require.True(t, fromA)

	// Paging of members.
	var seen int
	cursor := ""
	for {
		q := path + "?limit=2"
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		page := decode[gen.MemberPage](t, admin.Get(q))
		seen += len(page.Items)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	require.Equal(t, len(own.Items), seen)
	admin.Get(path+"?limit=2&cursor=junk").RequireProblem(http.StatusBadRequest, "invalid_cursor")

	// Change the role, then remove.
	userPath := nodeURL(w.A1, "/members/"+m.UserId.String())
	changed := decode[gen.Member](t, admin.Put(userPath, map[string]string{"role": "analyst"}))
	require.Equal(t, gen.Role("analyst"), changed.Role)
	admin.Put(userPath, map[string]string{"role": "owner"}).RequireProblem(http.StatusForbidden, "role_not_grantable")
	admin.Put(nodeURL(w.A1, "/members/"+uuid.NewString()), map[string]string{"role": "viewer"}).
		RequireProblem(http.StatusNotFound, "not_found")
	requireStatus(t, admin.Delete(userPath), http.StatusNoContent)
	admin.Delete(userPath).RequireProblem(http.StatusNotFound, "not_found")
	newbie.Get(nodeURL(w.A1, "")).RequireProblem(http.StatusNotFound, "not_found")
	require.NotContains(t, s.Log.String(), newPassword12)
}

// Proposed ID (not in spec 08 yet): T-A-41
// EC-35: the last owner of the root cannot be removed or demoted.
func TestAPILastRootOwner(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	owner := s.As(w.OwnerRoot)
	self := nodeURL(w.Root, "/members/"+w.OwnerRoot.String())

	owner.Delete(self).RequireProblem(http.StatusConflict, "last_root_owner")
	owner.Put(self, map[string]string{"role": "admin"}).RequireProblem(http.StatusConflict, "last_root_owner")
	requireStatus(t, owner.Get(nodeURL(w.Root, "")), http.StatusOK)
}
