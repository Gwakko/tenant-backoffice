//go:build integration

package apitest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

var keySeq atomic.Int64

// newKey is an idempotency key that no other call of the process uses.
func newKey(prefix string) string { return fmt.Sprintf("%s-%d", prefix, keySeq.Add(1)) }

// emptyKey sends an Idempotency-Key header with no value (WithHeader would
// remove it instead).
func emptyKey() apitest.Option {
	return func(r *http.Request) { r.Header["Idempotency-Key"] = []string{""} }
}

func credit(c *apitest.Client, node uuid.UUID, amount, key string) *apitest.Response {
	return c.Post(nodeURL(node, "/credits"), map[string]string{"amount": amount}, apitest.WithIdempotencyKey(key))
}

func debit(c *apitest.Client, node uuid.UUID, amount, key string) *apitest.Response {
	return c.Post(nodeURL(node, "/debits"), map[string]string{"amount": amount}, apitest.WithIdempotencyKey(key))
}

func transfer(c *apitest.Client, from, to uuid.UUID, amount, key string) *apitest.Response {
	return c.Post(api+"/transfers", map[string]string{
		"fromNodeId": from.String(), "toNodeId": to.String(), "amount": amount,
	}, apitest.WithIdempotencyKey(key))
}

func mustOperation(t *testing.T, res *apitest.Response) gen.Operation {
	t.Helper()
	requireStatus(t, res, http.StatusCreated)
	return decode[gen.Operation](t, res)
}

func operationsPage(t *testing.T, c *apitest.Client, query string) gen.OperationPage {
	t.Helper()
	res := c.Get(api + "/operations?" + query)
	requireStatus(t, res, http.StatusOK)
	return decode[gen.OperationPage](t, res)
}

func auditPage(t *testing.T, c *apitest.Client, query string) gen.AuditPage {
	t.Helper()
	res := c.Get(api + "/audit?" + query)
	requireStatus(t, res, http.StatusOK)
	return decode[gen.AuditPage](t, res)
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ledgerState is a fingerprint of everything an operation could change: the
// ledger, the balances, the journal and the idempotency keys.
type ledgerState struct {
	Ops, Audit, Keys int
	BalanceSum       int
	Versions         int
}

func stateOf(t *testing.T, s *apitest.Stack) ledgerState {
	t.Helper()
	return ledgerState{
		Ops:        count(t, s, `SELECT count(*) FROM operations`),
		Audit:      count(t, s, `SELECT count(*) FROM audit_log`),
		Keys:       count(t, s, `SELECT count(*) FROM idempotency_keys`),
		BalanceSum: count(t, s, `SELECT coalesce(sum(amount), 0)::int FROM balances`),
		Versions:   count(t, s, `SELECT coalesce(sum(version), 0)::int FROM balances`),
	}
}

func balanceOf(t *testing.T, c *apitest.Client, node uuid.UUID) string {
	t.Helper()
	res := c.Get(nodeURL(node, "/balance"))
	requireStatus(t, res, http.StatusOK)
	return decode[gen.Balance](t, res).Amount
}

// insertOperations writes n credits with one and the same created_at as the
// table owner (the fixture side), to meet the tie of the keyset order.
func insertOperations(t *testing.T, s *apitest.Stack, node, actor uuid.UUID, n int, at time.Time) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.Must(uuid.NewV7())
		_, err := s.DB.Owner.Exec(context.Background(),
			`INSERT INTO operations (id, node_id, kind, amount, balance_after, actor_id, created_at)
			 VALUES ($1, $2, 'credit', 1, 1, $3, $4)`, ids[i], node, actor, at)
		require.NoError(t, err)
	}
	return ids
}

// logRecords parses the JSON log lines the API wrote.
func logRecords(t *testing.T, s *apitest.Stack) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.Log.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m), line)
		out = append(out, m)
	}
	return out
}

// refusalLog is the "request refused" record of a request id, nil if absent.
func refusalLog(t *testing.T, s *apitest.Stack, requestID string) map[string]any {
	t.Helper()
	for _, m := range logRecords(t, s) {
		if m["msg"] == "request refused" && m["request_id"] == requestID {
			return m
		}
	}
	return nil
}
