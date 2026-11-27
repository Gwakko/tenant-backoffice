//go:build integration

package apitest_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

// Test-ID: T-A-10
// EC-65: the same key and the same body give the same 201 and the same body,
// marked as a replay; one operation, the balance changes once. Field order and
// an absent comment against an empty one do not change the meaning.
func TestAPIReplaySameBody(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	key := newKey("replay")

	first := admin.Post(nodeURL(w.A1u1, "/credits"), `{"amount":"100","comment":""}`, apitest.WithIdempotencyKey(key))
	requireStatus(t, first, http.StatusCreated)
	assert.Empty(t, first.Header.Get("Idempotent-Replayed"))
	before := stateOf(t, s)

	second := admin.Post(nodeURL(w.A1u1, "/credits"), `{ "amount" : "100" }`, apitest.WithIdempotencyKey(key))
	requireStatus(t, second, http.StatusCreated)
	assert.Equal(t, "true", second.Header.Get("Idempotent-Replayed"))
	assert.JSONEq(t, string(first.Body), string(second.Body), "the same operation comes back")
	assert.Equal(t, before, stateOf(t, s), "a replay writes nothing")
	assert.Equal(t, "1200", balanceOf(t, admin, w.A1u1), "the balance rose once")

	// A transfer replays with both legs.
	tkey := newKey("replay-t")
	t1 := transfer(admin, w.A1u2, w.A1u1, "50", tkey)
	requireStatus(t, t1, http.StatusCreated)
	t2 := transfer(admin, w.A1u2, w.A1u1, "50", tkey)
	requireStatus(t, t2, http.StatusCreated)
	assert.Equal(t, "true", t2.Header.Get("Idempotent-Replayed"))
	assert.JSONEq(t, string(t1.Body), string(t2.Body))
	assert.Equal(t, 2, count(t, s, `SELECT count(*) FROM operations WHERE transfer_id = $1`, decode[gen.Transfer](t, t1).TransferId))
}

// Test-ID: T-A-11
// EC-66, EC-69: the same key with another amount, another node, another route
// or another comment is 422 idempotency_key_reused, and no second operation
// appears.
func TestAPIReuseDifferentBody(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	key := newKey("reuse")

	mustOperation(t, credit(admin, w.A1u1, "100", key))
	before := stateOf(t, s)

	credit(admin, w.A1u1, "200", key).RequireProblem(http.StatusUnprocessableEntity, "idempotency_key_reused")
	credit(admin, w.A1u2, "100", key).RequireProblem(http.StatusUnprocessableEntity, "idempotency_key_reused")
	debit(admin, w.A1u1, "100", key).RequireProblem(http.StatusUnprocessableEntity, "idempotency_key_reused")
	transfer(admin, w.A1u2, w.A1u1, "100", key).RequireProblem(http.StatusUnprocessableEntity, "idempotency_key_reused")
	res := admin.Post(nodeURL(w.A1u1, "/credits"), map[string]string{"amount": "100", "comment": "other"}, apitest.WithIdempotencyKey(key))
	res.RequireProblem(http.StatusUnprocessableEntity, "idempotency_key_reused")

	assert.Equal(t, before, stateOf(t, s))
	assert.Equal(t, "1200", balanceOf(t, admin, w.A1u1))
}

// Test-ID: T-A-12
// EC-70: a missing key is idempotency_key_required, an empty, too long or
// malformed one is idempotency_key_invalid; the use case is never reached.
func TestAPIKeyMissingOrInvalid(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	before := stateOf(t, s)

	body := map[string]string{"amount": "1"}
	tbody := map[string]string{"fromNodeId": w.A1u2.String(), "toNodeId": w.A1u1.String(), "amount": "1"}
	routes := map[string]struct {
		path string
		body any
	}{
		"credit":   {nodeURL(w.A1u1, "/credits"), body},
		"debit":    {nodeURL(w.A1u1, "/debits"), body},
		"transfer": {api + "/transfers", tbody},
	}
	for name, r := range routes {
		admin.Post(r.path, r.body).RequireProblem(http.StatusBadRequest, "idempotency_key_required")
		invalid := map[string]apitest.Option{
			"empty":     emptyKey(),
			"129":       apitest.WithIdempotencyKey(strings.Repeat("a", 129)),
			"space":     apitest.WithIdempotencyKey("a b"),
			"slash":     apitest.WithIdempotencyKey("a/b"),
			"non-ascii": apitest.WithIdempotencyKey("ключ"),
		}
		for kind, opt := range invalid {
			res := admin.Post(r.path, r.body, opt)
			require.Equal(t, http.StatusBadRequest, res.Status, "%s %s: %s", name, kind, res.Body)
			assert.Equal(t, "idempotency_key_invalid", res.Problem().Code, "%s %s", name, kind)
		}
	}
	assert.Equal(t, before, stateOf(t, s), "no refused request reached the use case")

	// 128 characters of the allowed alphabet is the longest valid key.
	longest := strings.Repeat("k", 100) + strings.Repeat(":._-", 7)
	require.Len(t, longest, 128)
	requireStatus(t, admin.Post(nodeURL(w.A1u1, "/credits"), body, apitest.WithIdempotencyKey(longest)), http.StatusCreated)
}

// Test-ID: T-A-52
// EC-67, EC-68: concurrent identical requests with one key make one operation
// and every other answer is its replay; the same key of another user is an
// independent request.
func TestAPIConcurrentSameKey(t *testing.T) {
	s := apitest.NewStack(t)
	w := s.World
	admin := s.As(w.AdminA)
	key := newKey("race")
	before := stateOf(t, s)

	const n = 6
	results := make([]*apitest.Response, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = credit(admin, w.A1u1, "10", key)
		}()
	}
	close(start)
	wg.Wait()

	replays := 0
	var first []byte
	for i, r := range results {
		require.Equal(t, http.StatusCreated, r.Status, "request %d: %s", i, r.Body)
		if r.Header.Get("Idempotent-Replayed") == "true" {
			replays++
		}
		if first == nil {
			first = r.Body
		}
		assert.JSONEq(t, string(first), string(r.Body), "every answer is the same operation")
	}
	assert.Equal(t, n-1, replays, "exactly one request did the work")
	assert.Equal(t, before.Ops+1, stateOf(t, s).Ops)
	assert.Equal(t, "1110", balanceOf(t, admin, w.A1u1))

	// Another user with the same key text: an independent operation.
	owner := s.As(w.OwnerRoot)
	other := credit(owner, w.A1u1, "10", key)
	requireStatus(t, other, http.StatusCreated)
	assert.Empty(t, other.Header.Get("Idempotent-Replayed"))
	assert.Equal(t, "1120", balanceOf(t, admin, w.A1u1))
}
