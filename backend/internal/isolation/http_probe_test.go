//go:build integration

package isolation_test

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
)

// allowedHeaders are the only headers any answer of the API may carry. A header
// that counted queries, measured time or named a server would let a client tell
// a foreign node from a missing one by something other than the body.
var allowedHeaders = map[string]bool{
	"Cache-Control": true, "Content-Length": true, "Content-Type": true, "Date": true,
	"X-Content-Type-Options": true, "X-Request-Id": true, "Allow": true,
}

// trackHeaders wraps ops so that every header name of every answer is recorded.
func trackHeaders(ops []hOp, seen map[string]bool) []hOp {
	out := make([]hOp, len(ops))
	for i, op := range ops {
		call := op.call
		op.call = func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			r := call(c, id)
			for k := range r.Header {
				seen[http.CanonicalHeaderKey(k)] = true
			}
			return r
		}
		out[i] = op
	}
	return out
}

// Test-ID: T-ISO-09
// EC-54: enumeration of ids. 100 ids (50 real nodes of branch B, 50 random: 25
// UUIDv4 and 25 UUIDv7) are asked on read operations of the node, its children,
// balance, operations, audit, reports and members. The answers are one and the
// same, byte for byte, for real and random ids; the headers are a fixed small
// set (nothing counts queries or time); nothing changes. Invalid ids are 400
// invalid_id for every actor alike. The time of the answers is not examined.
func TestHTTPIDEnumeration(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	ctx := t.Context()
	// 4 nodes of the fixture and 46 added: 50 real nodes of B.
	realIDs := append([]uuid.UUID{}, w.NodesB...)
	for i := range 46 {
		realIDs = append(realIDs, w.AddNode(ctx, t, "unit", &w.B1, fmt.Sprintf("B-extra-%02d", i)))
	}
	require.Len(t, realIDs, 50)
	var random []uuid.UUID
	for range 25 {
		random = append(random, uuid.New(), uuid.Must(uuid.NewV7()))
	}
	all := append(append([]uuid.UUID{}, realIDs...), random...)
	require.Len(t, all, 100)
	before := h.snap()

	enumOps := append(
		only(nodeReadOps(), "node", "children", "members"),
		only(ledgerReadOps(hActor{scope: w.A}), "balance", "operations scope", "audit scope", "report")...) // own scope fixed to A
	headers := map[string]bool{}
	adminA := h.actors()[0]
	t.Run("admin of A, every operation", func(t *testing.T) {
		requireAllLikeMissing(t, adminA, trackHeaders(enumOps, headers), all)
	})
	for _, a := range []hActor{h.readers()[0], h.readers()[2]} {
		t.Run(a.name+", node, children, balance", func(t *testing.T) {
			ops := only(enumOps, "node", "children", "balance")
			requireAllLikeMissing(t, a, trackHeaders(ops, headers), all)
		})
	}
	var names []string
	for k := range headers {
		names = append(names, k)
		assert.True(t, allowedHeaders[k], "header %q is not in the allowed set", k)
	}
	sort.Strings(names)
	t.Logf("header names seen: %v", names)

	// Control: the admin of B does see what the admin of A is told does not exist.
	for _, id := range realIDs {
		require.Equal(t, http.StatusOK, h.as(w.AdminB).Get(hNodePath(id, "")).Status, id.String())
	}

	// Other spellings of the same ids: the same answer for a real and a random id.
	spell := map[string]func(uuid.UUID) string{
		"upper case":  func(id uuid.UUID) string { return strings.ToUpper(id.String()) },
		"no hyphens":  func(id uuid.UUID) string { return strings.ReplaceAll(id.String(), "-", "") },
		"braces":      func(id uuid.UUID) string { return url.PathEscape("{" + id.String() + "}") },
		"urn":         func(id uuid.UUID) string { return "urn:uuid:" + id.String() },
		"trailing":    func(id uuid.UUID) string { return id.String() + "%20" },
		"percent-hex": func(id uuid.UUID) string { return strings.Replace(id.String(), "-", "%2D", 1) },
	}
	for name, f := range spell {
		missing := adminA.c.Get(hAPI + "/nodes/" + f(uuid.New()))
		for _, id := range []uuid.UUID{w.B1, w.B1u1, realIDs[10], w.Root} {
			requireSameAnswer(t, name, missing, adminA.c.Get(hAPI+"/nodes/"+f(id)))
		}
	}

	// Invalid ids and the zero id: 400 for every actor, the same answer for all of them.
	for _, raw := range []string{
		"not-a-uuid", "1", "0", "null", "undefined", "-1", "%27%20OR%201%3D1%20--",
		strings.Repeat("a", 300), "00000000-0000-0000-0000-000000000000",
	} {
		var first *apitest.Response
		for _, a := range append(h.actors(), h.readers()...) {
			r := a.c.Get(hAPI + "/nodes/" + raw)
			require.Equal(t, http.StatusBadRequest, r.Status, "%q: %s", raw, r.Body)
			if first == nil {
				first = r
				continue
			}
			requireSameAnswer(t, "invalid id "+raw+" for "+a.name, first, r)
		}
	}
	h.requireUnchanged(before, "enumeration changed data")
	requireNoLogErrors(t, h.s)
}

// The headers and CORS of an answer must not tell a foreign node from a
// missing one: no CORS header on either (the API is same-origin behind the
// proxy), the security headers on both, and HEAD, OPTIONS and a method the
// route does not have answer the same for both.
func TestHTTPForeignAnswerHeadersAndMethods(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	a := h.actors()[0]
	before := h.snap()

	t.Run("security headers and cors", func(t *testing.T) {
		origin := apitest.WithHeader("Origin", "https://attacker.example")
		preflight := apitest.WithHeader("Access-Control-Request-Method", "DELETE")
		missing := a.c.Get(hNodePath(uuid.New(), ""), origin)
		foreign := a.c.Get(hNodePath(w.B1, ""), origin)
		requireSameAnswer(t, "with an Origin", missing, foreign)
		for _, r := range []*apitest.Response{missing, foreign} {
			assert.Equal(t, "nosniff", r.Header.Get("X-Content-Type-Options"))
			assert.Equal(t, "no-store", r.Header.Get("Cache-Control"))
			for k := range r.Header {
				assert.False(t, strings.HasPrefix(http.CanonicalHeaderKey(k), "Access-Control-"), "unexpected %s", k)
			}
		}
		for _, id := range []uuid.UUID{uuid.New(), w.B1, w.B, w.Root} {
			r := a.c.Do(http.MethodOptions, hNodePath(id, ""), nil, origin, preflight)
			requireSameAnswer(t, "preflight", a.c.Do(http.MethodOptions, hNodePath(uuid.New(), ""), nil, origin, preflight), r)
			assert.Empty(t, r.Header.Get("Access-Control-Allow-Origin"))
		}
		// The control: an own node has the same security headers.
		own := a.c.Get(hNodePath(w.A1, ""))
		assert.Equal(t, missing.Header.Get("Cache-Control"), own.Header.Get("Cache-Control"))
		assert.Equal(t, missing.Header.Get("X-Content-Type-Options"), own.Header.Get("X-Content-Type-Options"))
	})

	t.Run("HEAD, OPTIONS and other methods", func(t *testing.T) {
		paths := []string{
			"", "/children", "/move", "/move-targets", "/deactivate", "/activate", "/settings",
			"/settings/operations_enabled", "/members", "/members/" + w.OnlyB.String(), "/balance", "/credits", "/debits",
			"/reports/summary?" + reportQuery(), "/reports/children?" + reportQuery(),
		}
		for _, p := range paths {
			for _, method := range []string{http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodConnect, "FOO"} {
				missing := a.c.Do(method, hNodePath(uuid.New(), p), nil)
				for _, id := range []uuid.UUID{w.B1, w.B1u1, w.Root} {
					requireSameAnswer(t, method+" "+p, missing, a.c.Do(method, hNodePath(id, p), nil))
				}
			}
		}
		for _, q := range []string{"/operations?scopeNodeId=%s", "/audit?scopeNodeId=%s"} {
			for _, method := range []string{http.MethodHead, http.MethodOptions} {
				missing := a.c.Do(method, hAPI+fmt.Sprintf(q, uuid.New()), nil)
				requireSameAnswer(t, method+" "+q, missing, a.c.Do(method, hAPI+fmt.Sprintf(q, w.B1), nil))
			}
		}
		// HEAD of a read is the status of the GET and carries no body.
		head := a.c.Do(http.MethodHead, hNodePath(w.B1, ""), nil)
		assert.Equal(t, http.StatusNotFound, head.Status)
		assert.Empty(t, head.Body)
	})
	h.requireUnchanged(before, "methods changed data")
}

// A foreign id in the body or the query is the answer of a missing id: the
// destination of a transfer, the new parent of a move, ids in unknown fields
// that are not part of the contract, and the filters of the lists. Nothing
// is executed.
func TestHTTPForeignIDInRequest(t *testing.T) {
	h := newHWorld(t)
	w := h.w
	admin := h.as(w.AdminA)
	before := h.snap()

	same := func(label string, call func(id uuid.UUID) *apitest.Response, foreign ...uuid.UUID) {
		t.Helper()
		missing := call(uuid.New())
		for _, id := range foreign {
			requireSameAnswer(t, label, missing, call(id))
		}
	}
	foreign := []uuid.UUID{w.B, w.B1, w.B1u1, w.Root}
	same("transfer toNodeId", func(id uuid.UUID) *apitest.Response {
		return admin.Post(hAPI+"/transfers", map[string]string{
			"fromNodeId": w.A1u1.String(), "toNodeId": id.String(), "amount": "1",
		}, apitest.WithIdempotencyKey(hKey()))
	}, foreign...)
	same("transfer fromNodeId", func(id uuid.UUID) *apitest.Response {
		return admin.Post(hAPI+"/transfers", map[string]string{
			"fromNodeId": id.String(), "toNodeId": w.A1u1.String(), "amount": "1",
		}, apitest.WithIdempotencyKey(hKey()))
	}, foreign...)
	same("move newParentId", func(id uuid.UUID) *apitest.Response {
		return admin.Post(hNodePath(w.A1u2, "/move"), map[string]any{"newParentId": id})
	}, foreign...)
	same("create child with a parentId in the body", func(id uuid.UUID) *apitest.Response {
		return admin.Post(hNodePath(w.A1, "/children"), map[string]any{"type": "unit", "name": "X", "parentId": id})
	}, foreign...)
	same("credit with a nodeId in the body", func(id uuid.UUID) *apitest.Response {
		return admin.Post(hNodePath(w.A1u1, "/credits"), map[string]any{"amount": "1", "nodeId": id}, apitest.WithIdempotencyKey(hKey()))
	}, foreign...)
	same("children of an own node with a nodeId query", func(id uuid.UUID) *apitest.Response {
		return admin.Get(hNodePath(w.A1, "/children?nodeId="+id.String()))
	}, foreign...)
	same("operations of an own scope, node filter", func(id uuid.UUID) *apitest.Response {
		return admin.Get(hAPI + "/operations?scopeNodeId=" + w.A.String() + "&nodeId=" + id.String())
	}, foreign...)
	same("audit of an own scope, node filter", func(id uuid.UUID) *apitest.Response {
		return admin.Get(hAPI + "/audit?scopeNodeId=" + w.A.String() + "&nodeId=" + id.String())
	}, foreign...)
	same("members: the subject of a role change is a foreign user", func(id uuid.UUID) *apitest.Response {
		return admin.Put(hNodePath(w.A1, "/members/"+id.String()), map[string]string{"role": "viewer"})
	}, w.AdminB, w.OnlyB, w.OwnerRoot)
	same("members: the subject of a removal is a foreign user", func(id uuid.UUID) *apitest.Response {
		return admin.Delete(hNodePath(w.A1, "/members/"+id.String()))
	}, w.AdminB, w.OnlyB, w.OwnerRoot)
	same("audit filtered by a foreign actor", func(id uuid.UUID) *apitest.Response {
		return admin.Get(hAPI + "/audit?scopeNodeId=" + w.A.String() + "&actorId=" + id.String())
	}, w.AdminB, w.OwnerRoot)
	h.requireUnchanged(before, "a foreign id in a request changed data")
	requireNoLogErrors(t, h.s)
}
