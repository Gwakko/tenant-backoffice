//go:build integration

package isolation_test

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/isolation"
)

// The HTTP variants of the isolation tests (spec 08 section 3.3) run on the
// real API (apitest.Stack: real use cases, repositories, transactions and
// middleware over tb_app). The criterion is the same everywhere: the answer
// about a node of another branch is the answer about a node that does not
// exist, byte for byte: status, every header (except the request id and the
// date) and the body (except requestId); and the data does not change.

const (
	hAPI        = "/api/v1"
	hNewUserPwd = "a-long-initial-password-1"
)

func hNodePath(id uuid.UUID, suffix string) string { return hAPI + "/nodes/" + id.String() + suffix }

var hKeySeq atomic.Int64

// hKey is an Idempotency-Key that no other call of the process uses.
func hKey() string { return fmt.Sprintf("iso-%d-%s", hKeySeq.Add(1), uuid.NewString()[:8]) }

// volatileHeaders differ between any two answers by design.
var volatileHeaders = map[string]bool{"X-Request-Id": true, "Date": true}

// canonicalAnswer renders status, all stable headers and the body without
// requestId into one string, so two answers are equal if and only if a client
// could not tell them apart.
func canonicalAnswer(r *apitest.Response) string {
	var b strings.Builder
	fmt.Fprintf(&b, "status %d\n", r.Status)
	names := make([]string, 0, len(r.Header))
	for k := range r.Header {
		if !volatileHeaders[http.CanonicalHeaderKey(k)] {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Fprintf(&b, "%s: %s\n", k, strings.Join(r.Header.Values(k), ", "))
	}
	b.WriteString("\n" + r.BodyWithoutRequestID())
	return b.String()
}

// requireSameAnswer fails unless got is byte for byte the answer want is.
func requireSameAnswer(t *testing.T, label string, want, got *apitest.Response) {
	t.Helper()
	require.Equal(t, canonicalAnswer(want), canonicalAnswer(got), label)
	require.NotEmpty(t, got.Header.Get("X-Request-Id"), label)
	require.NotEqual(t, want.Header.Get("X-Request-Id"), got.Header.Get("X-Request-Id"), label)
}

// canonicalNotFound is the one body every 404 not_found of the API has (the
// fixed text of the problem table, with requestId blanked), whatever the route.
const canonicalNotFound = `{"type":"https://tenant-backoffice.invalid/problems/not_found","title":"Not found","status":404,` +
	`"code":"not_found","detail":"The object was not found.","requestId":""}`

// notFoundHeaders is the exact set of headers of that answer.
var notFoundHeaders = []string{"Cache-Control", "Content-Length", "Content-Type", "Date", "X-Content-Type-Options", "X-Request-Id"}

// requireHTTPNotFound asserts the one answer a foreign or missing node gets:
// the 404, the canonical body and exactly the canonical headers, so that a
// route that words its 404 differently, or adds a header to it, is found even
// when its foreign and its missing answers are alike.
func requireHTTPNotFound(t *testing.T, label string, r *apitest.Response) {
	t.Helper()
	require.Equal(t, http.StatusNotFound, r.Status, "%s: %s", label, r.Body)
	require.Equal(t, "not_found", r.Problem().Code, label)
	require.Equal(t, canonicalNotFound, r.BodyWithoutRequestID(), label)
	var names []string
	for k := range r.Header {
		names = append(names, http.CanonicalHeaderKey(k))
	}
	sort.Strings(names)
	require.Equal(t, notFoundHeaders, names, "%s: the headers of a 404", label)
	require.Equal(t, "no-store", r.Header.Get("Cache-Control"), label)
	require.Equal(t, "nosniff", r.Header.Get("X-Content-Type-Options"), label)
}

// hOp is one request about a node id.
type hOp struct {
	name string
	// ownScope marks an op that also touches an own node (a scope, a filter, a
	// second node): for a role too low for it the answer is forbidden even for a
	// missing id, so only the equality with the answer about a missing id is required.
	ownScope bool
	call     func(c *apitest.Client, id uuid.UUID) *apitest.Response
}

// hActor is a signed-in user with the nodes the table of operations needs.
type hActor struct {
	name  string
	user  uuid.UUID
	c     *apitest.Client
	scope uuid.UUID // a node the actor reads, the scope of its own listings
	unit  uuid.UUID // an own leaf the actor may move money on
	unit2 uuid.UUID // another own leaf
	// foreign are the nodes that must look as missing to the actor.
	foreign []uuid.UUID
}

// hWorld is a stack with every World user signed in before the first snapshot
// (a login writes a refresh token, so it must not fall between two snapshots).
type hWorld struct {
	t       *testing.T
	s       *apitest.Stack
	w       *isolation.World
	clients map[uuid.UUID]*apitest.Client
}

func newHWorld(t *testing.T, opts ...apitest.StackOption) *hWorld {
	t.Helper()
	s := apitest.NewStack(t, opts...)
	h := &hWorld{t: t, s: s, w: s.World, clients: map[uuid.UUID]*apitest.Client{}}
	for _, u := range []uuid.UUID{
		h.w.OwnerRoot, h.w.AdminA, h.w.AnalystA, h.w.ViewerA1u1, h.w.AdminB, h.w.OnlyB, h.w.Multi, h.w.Orphan,
	} {
		h.clients[u] = s.As(u)
	}
	return h
}

func (h *hWorld) as(u uuid.UUID) *apitest.Client {
	h.t.Helper()
	c, ok := h.clients[u]
	require.True(h.t, ok, "user was not signed in up front")
	return c
}

// snap is the digest of every table (isolation.Snapshot).
func (h *hWorld) snap() string {
	h.t.Helper()
	return isolation.Snapshot(h.t.Context(), h.t, h.s.DB)
}

// requireUnchanged fails, naming the tables that differ, unless every table is
// as it was in before.
func (h *hWorld) requireUnchanged(before, msg string) {
	h.t.Helper()
	after := h.snap()
	if before == after {
		return
	}
	b, a := strings.Split(before, "|"), strings.Split(after, "|")
	var changed []string
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			changed = append(changed, strings.SplitN(a[i], "=", 2)[0])
		}
	}
	h.t.Fatalf("%s: tables changed: %v", msg, changed)
}

// actors are the readers and writers of the tables of operations.
func (h *hWorld) actors() []hActor {
	w := h.w
	withRoot := func(ids ...uuid.UUID) []uuid.UUID { return append(ids, w.Root) }
	return []hActor{
		{
			name: "admin of A", user: w.AdminA, c: h.as(w.AdminA), scope: w.A, unit: w.A1u1, unit2: w.A1u2,
			foreign: withRoot(w.NodesB...),
		},
		{
			name: "admin of B", user: w.AdminB, c: h.as(w.AdminB), scope: w.B, unit: w.B1u1, unit2: w.B1u2,
			foreign: withRoot(w.NodesA...),
		},
		// Administers A1 and views B1: B1 is visible to it, everything above is not.
		{
			name: "multi", user: w.Multi, c: h.as(w.Multi), scope: w.A1, unit: w.A1u1, unit2: w.A1u2,
			foreign: []uuid.UUID{w.Root, w.A, w.B},
		},
	}
}

// readers are actors that only read: roles too low for writing or no roles.
func (h *hWorld) readers() []hActor {
	w := h.w
	return []hActor{
		{
			name: "viewer of a unit", user: w.ViewerA1u1, c: h.as(w.ViewerA1u1), scope: w.A1u1, unit: w.A1u1, unit2: w.A1u1,
			foreign: append([]uuid.UUID{w.Root, w.A, w.A1, w.A1u2}, w.NodesB...),
		},
		{
			name: "analyst of a company", user: w.AnalystA, c: h.as(w.AnalystA), scope: w.A1, unit: w.A1u1, unit2: w.A1u2,
			foreign: append([]uuid.UUID{w.Root, w.A}, w.NodesB...),
		},
		{
			name: "user without roles", user: w.Orphan, c: h.as(w.Orphan), scope: w.A, unit: w.A1u1, unit2: uuid.New(),
			foreign: w.AllNodes,
		},
	}
}

func reportQuery() string {
	now := time.Now().UTC()
	q := url.Values{
		"from": {now.AddDate(0, 0, -1).Format(time.DateOnly)},
		"to":   {now.AddDate(0, 0, 2).Format(time.DateOnly)},
		"tz":   {"UTC"},
	}
	return q.Encode()
}

// --- the tables of operations -------------------------------------------------

func getOp(name, suffix string) hOp {
	return hOp{name: name, call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
		return c.Get(hNodePath(id, suffix))
	}}
}

// nodeReadOps are the reads of the node, its children, move targets, members,
// settings and permissions.
func nodeReadOps() []hOp {
	return []hOp{
		getOp("node", ""),
		getOp("children", "/children"),
		getOp("children page with search", "/children?limit=1&q=a"),
		getOp("move-targets", "/move-targets"),
		getOp("members", "/members"),
		getOp("members inherited", "/members?inherited=true"),
		getOp("settings", "/settings"),
		{name: "permissions", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Get(hAPI + "/me/permissions?nodeId=" + id.String())
		}},
	}
}

func ledgerReadOps(a hActor) []hOp {
	return []hOp{
		getOp("balance", "/balance"),
		{name: "operations scope", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Get(hAPI + "/operations?scopeNodeId=" + id.String())
		}},
		{name: "operations node filter", ownScope: true, call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Get(hAPI + "/operations?scopeNodeId=" + a.scope.String() + "&nodeId=" + id.String())
		}},
		{name: "audit scope", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Get(hAPI + "/audit?scopeNodeId=" + id.String())
		}},
		{name: "audit node filter", ownScope: true, call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Get(hAPI + "/audit?scopeNodeId=" + a.scope.String() + "&nodeId=" + id.String())
		}},
		getOp("report summary", "/reports/summary?"+reportQuery()),
		getOp("report children", "/reports/children?"+reportQuery()),
	}
}

// nodeWriteOps are the mutations of a node and of its members and settings.
func nodeWriteOps(a hActor) []hOp {
	return []hOp{
		{name: "rename", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Patch(hNodePath(id, ""), map[string]string{"name": "Renamed"})
		}},
		{name: "deactivate", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Post(hNodePath(id, "/deactivate"), nil)
		}},
		{name: "activate", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Post(hNodePath(id, "/activate"), nil)
		}},
		{name: "delete", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Delete(hNodePath(id, ""))
		}},
		{name: "create child", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Post(hNodePath(id, "/children"), map[string]string{"type": "unit", "name": "Planted"})
		}},
		{name: "move the node away", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Post(hNodePath(id, "/move"), map[string]any{"newParentId": a.scope})
		}},
		{name: "move an own node under it", ownScope: true, call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Post(hNodePath(a.unit2, "/move"), map[string]any{"newParentId": id})
		}},
		{name: "settings put", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Put(hNodePath(id, "/settings/operations_enabled"), map[string]any{"value": false})
		}},
		{name: "settings delete", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Delete(hNodePath(id, "/settings/operations_enabled"))
		}},
	}
}

func memberWriteOps(w *isolation.World) []hOp {
	return []hOp{
		{name: "members add new user", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Post(hNodePath(id, "/members"), map[string]any{
				"email": "planted@example.test", "role": "viewer", "initialPassword": hNewUserPwd,
			})
		}},
		{name: "members add existing user of the other branch", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Post(hNodePath(id, "/members"), map[string]any{
				"email": w.Emails[w.OnlyB], "role": "analyst", "initialPassword": hNewUserPwd,
			})
		}},
		{name: "members change role", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Put(hNodePath(id, "/members/"+w.OnlyB.String()), map[string]string{"role": "admin"})
		}},
		{name: "members change role of nobody", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Put(hNodePath(id, "/members/"+uuid.NewString()), map[string]string{"role": "admin"})
		}},
		{name: "members remove", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Delete(hNodePath(id, "/members/"+w.OnlyB.String()))
		}},
		{name: "members remove nobody", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Delete(hNodePath(id, "/members/"+uuid.NewString()))
		}},
	}
}

func moneyWriteOps(a hActor) []hOp {
	transfer := func(c *apitest.Client, from, to uuid.UUID, key string) *apitest.Response {
		return c.Post(hAPI+"/transfers", map[string]string{
			"fromNodeId": from.String(), "toNodeId": to.String(), "amount": "1",
		}, apitest.WithIdempotencyKey(key))
	}
	return []hOp{
		{name: "credit", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Post(hNodePath(id, "/credits"), map[string]string{"amount": "1"}, apitest.WithIdempotencyKey(hKey()))
		}},
		{name: "debit", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return c.Post(hNodePath(id, "/debits"), map[string]string{"amount": "1"}, apitest.WithIdempotencyKey(hKey()))
		}},
		{name: "transfer from it", ownScope: true, call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return transfer(c, id, a.unit2, hKey())
		}},
		{name: "transfer to it (toNodeId in the body)", ownScope: true, call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return transfer(c, a.unit2, id, hKey())
		}},
		{name: "transfer to a missing node", call: func(c *apitest.Client, id uuid.UUID) *apitest.Response {
			return transfer(c, id, uuid.New(), hKey())
		}},
	}
}

// requireAllLikeMissing runs every op of ops as the actor against every foreign
// node and against fresh ids (random v4 and v7), and requires one and the same
// answer, a 404 not_found. wantStatus can differ only for ops listed in
// exceptions (name -> status), where the answer must still be one and the same.
func requireAllLikeMissing(t *testing.T, a hActor, ops []hOp, foreign []uuid.UUID) {
	t.Helper()
	for _, op := range ops {
		missing := op.call(a.c, uuid.New())
		if !op.ownScope {
			requireHTTPNotFound(t, a.name+" / "+op.name+" / missing", missing)
		}
		v7 := op.call(a.c, uuid.Must(uuid.NewV7()))
		requireSameAnswer(t, a.name+" / "+op.name+" / v7 vs v4", missing, v7)
		for _, id := range foreign {
			got := op.call(a.c, id)
			if !op.ownScope {
				requireHTTPNotFound(t, fmt.Sprintf("%s / %s / %s", a.name, op.name, id), got)
			}
			requireSameAnswer(t, fmt.Sprintf("%s / %s / %s", a.name, op.name, id), missing, got)
		}
	}
}

// requireNoLogErrors fails if the API logged anything at error level: a
// foreign id is an ordinary not found, not an event to alarm on.
func requireNoLogErrors(t *testing.T, s *apitest.Stack) {
	t.Helper()
	for _, line := range strings.Split(s.Log.String(), "\n") {
		require.NotContains(t, line, `"level":"ERROR"`, "a foreign id must not be logged as an error")
	}
}

// requireNoneOf fails if body contains any of the strings.
func requireNoneOf(t *testing.T, label string, body []byte, forbidden []string) {
	t.Helper()
	for _, f := range forbidden {
		require.NotContains(t, string(body), f, label)
	}
}

func idStrings(groups ...[]uuid.UUID) []string {
	var out []string
	for _, g := range groups {
		for _, id := range g {
			out = append(out, id.String())
		}
	}
	return out
}

func hDecode[T any](t *testing.T, r *apitest.Response) T {
	t.Helper()
	require.Equal(t, http.StatusOK, r.Status, "body: %s", r.Body)
	var v T
	r.JSON(&v)
	return v
}

func hStatus(t *testing.T, r *apitest.Response, status int) {
	t.Helper()
	require.Equal(t, status, r.Status, "body: %s", r.Body)
}

// hCreate creates a child through the API and returns it.
func hCreate(t *testing.T, c *apitest.Client, parent uuid.UUID, typ, name string) gen.Node {
	t.Helper()
	r := c.Post(hNodePath(parent, "/children"), map[string]string{"type": typ, "name": name})
	hStatus(t, r, http.StatusCreated)
	var n gen.Node
	r.JSON(&n)
	return n
}
