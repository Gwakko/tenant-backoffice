//go:build integration

package apitest_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/config"
)

// Test-ID: T-A-18
// EC-113: a body above 64 KiB is 413 payload_too_large before any use case
// runs; the limit is on the bytes read, not on the declared length.
func TestAPIBodyTooLarge(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	before := stateOf(t, s)

	big := `{"amount":"1","comment":"` + strings.Repeat("a", 70*1024) + `"}`
	admin.Post(nodeURL(w.A1u1, "/credits"), big, apitest.WithIdempotencyKey(newKey("big"))).
		RequireProblem(http.StatusRequestEntityTooLarge, "payload_too_large")
	admin.Post(nodeURL(w.A1u1, "/debits"), big, apitest.WithIdempotencyKey(newKey("big"))).
		RequireProblem(http.StatusRequestEntityTooLarge, "payload_too_large")
	bigTransfer := `{"fromNodeId":"` + w.A1u2.String() + `","toNodeId":"` + w.A1u1.String() + `","amount":"1","comment":"` +
		strings.Repeat("a", 70*1024) + `"}`
	admin.Post(api+"/transfers", bigTransfer, apitest.WithIdempotencyKey(newKey("big"))).
		RequireProblem(http.StatusRequestEntityTooLarge, "payload_too_large")
	assert.Equal(t, before, stateOf(t, s))
}

// Test-ID: T-A-19
// EC-114: an unknown field is 400 validation_failed naming the field; this
// also closes the door to an actor or a balance smuggled into the body.
func TestAPIUnknownFields(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	before := stateOf(t, s)

	for name, res := range map[string]*apitest.Response{
		"credit": admin.Post(nodeURL(w.A1u1, "/credits"), `{"amount":"1","balanceAfter":"999999"}`, apitest.WithIdempotencyKey(newKey("u"))),
		"debit":  admin.Post(nodeURL(w.A1u1, "/debits"), `{"amount":"1","kind":"credit"}`, apitest.WithIdempotencyKey(newKey("u"))),
		"transfer": admin.Post(api+"/transfers", map[string]any{
			"fromNodeId": w.A1u2.String(), "toNodeId": w.A1u1.String(), "amount": "1", "currency": "USD",
		}, apitest.WithIdempotencyKey(newKey("u"))),
	} {
		p := res.RequireProblem(http.StatusBadRequest, "validation_failed")
		require.NotEmpty(t, p.Errors, name)
		assert.Equal(t, "unknown_field", p.Errors[0].Message, "%s: %s", name, res.Body)
	}
	assert.Equal(t, before, stateOf(t, s))
}

// Test-ID: T-A-20
// EC-115: an id that is not a UUID is 400 invalid_id in the path, the query
// and (as a field) the body, and is never looked up.
func TestAPIInvalidUUID(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	before := stateOf(t, s)

	for _, bad := range []string{"not-a-uuid", "123", "00000000-0000-0000-0000-00000000000g", uuid.NewString() + "0"} {
		for name, res := range map[string]*apitest.Response{
			"balance":       admin.Get(api + "/nodes/" + bad + "/balance"),
			"credit":        admin.Post(api+"/nodes/"+bad+"/credits", map[string]string{"amount": "1"}, apitest.WithIdempotencyKey(newKey("i"))),
			"debit":         admin.Post(api+"/nodes/"+bad+"/debits", map[string]string{"amount": "1"}, apitest.WithIdempotencyKey(newKey("i"))),
			"operation":     admin.Get(api + "/operations/" + bad),
			"ops scope":     admin.Get(api + "/operations?scopeNodeId=" + bad),
			"ops node":      admin.Get(api + "/operations?scopeNodeId=" + w.A.String() + "&nodeId=" + bad),
			"audit scope":   admin.Get(api + "/audit?scopeNodeId=" + bad),
			"audit actor":   admin.Get(api + "/audit?scopeNodeId=" + w.A.String() + "&actorId=" + bad),
			"audit node id": admin.Get(api + "/audit?scopeNodeId=" + w.A.String() + "&nodeId=" + bad),
		} {
			require.Equal(t, http.StatusBadRequest, res.Status, "%s %q: %s", name, bad, res.Body)
			assert.Equal(t, "invalid_id", res.Problem().Code, "%s %q", name, bad)
		}
	}
	tr := admin.Post(api+"/transfers", map[string]string{"fromNodeId": "x", "toNodeId": w.A1u1.String(), "amount": "1"},
		apitest.WithIdempotencyKey(newKey("i")))
	require.Equal(t, http.StatusBadRequest, tr.Status, string(tr.Body))
	assert.Contains(t, []string{"invalid_id", "validation_failed"}, tr.Problem().Code)
	assert.Equal(t, before, stateOf(t, s))
}

// Test-ID: T-A-21
// EC-116: writes are limited per user and minute: over the budget the answer
// is 429 rate_limited with Retry-After and nothing is written; reads and other
// users are not affected.
func TestAPIWriteRateLimit(t *testing.T) {
	s := apitest.NewStack(t, apitest.WithRate(config.RateLimit{
		LoginPairPerMin: 1000, LoginIPPerMin: 1000, RefreshIPPerMin: 1000, MutationPerMin: 3, ReadPerMin: 1000, MaxKeys: 1000,
	}))
	w := s.World
	admin, owner := s.As(w.AdminA), s.As(w.OwnerRoot)

	for range 3 {
		requireStatus(t, credit(admin, w.A1u1, "1", newKey("r")), http.StatusCreated)
	}
	before := stateOf(t, s)
	res := credit(admin, w.A1u1, "1", newKey("r"))
	res.RequireProblem(http.StatusTooManyRequests, "rate_limited")
	assert.NotEmpty(t, res.Header.Get("Retry-After"))
	transfer(admin, w.A1u2, w.A1u1, "1", newKey("r")).RequireProblem(http.StatusTooManyRequests, "rate_limited")
	assert.Equal(t, before, stateOf(t, s), "a limited request wrote nothing")

	requireStatus(t, admin.Get(nodeURL(w.A1u1, "/balance")), http.StatusOK)
	requireStatus(t, credit(owner, w.A1u1, "1", newKey("r")), http.StatusCreated)
}
