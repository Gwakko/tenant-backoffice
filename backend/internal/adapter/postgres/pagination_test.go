//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	bal "github.com/Gwakko/tenant-backoffice/backend/internal/usecase/balance"
)

// Test-ID: T-I-35
// Covers EC-92: the operations list is stable under inserts. The first page is read, five new
// operations arrive, and the second page continues with no duplicates and no gaps among the
// operations that existed when the first page was read.
func TestKeysetStableUnderInserts(t *testing.T) {
	w := newWorld(t)
	c := w.ledgerCases()
	list := bal.NewListOperations(c.deps)
	ctx := context.Background()

	const before = 12
	for i := range before {
		_, err := c.creditAs("adminA", "A1u1", "10", fmt.Sprintf("before-%d", i))
		require.NoError(t, err)
	}

	page := func(cursor string) bal.ListOperationsOutput {
		out, err := list.Execute(ctx, bal.ListOperationsInput{
			Actor: w.users["adminA"], ScopeNodeID: w.id("A1u1"), Limit: 5, Cursor: cursor,
		})
		require.NoError(t, err)
		return out
	}

	first := page("")
	require.Len(t, first.Items, 5)
	require.NotEmpty(t, first.NextCursor)

	// New operations are newer than everything the first page showed.
	for i := range 5 {
		_, err := c.creditAs("adminA", "A1u1", "7", fmt.Sprintf("during-%d", i))
		require.NoError(t, err)
	}

	seen := map[string]int{}
	for _, it := range first.Items {
		seen[fmt.Sprint(it.Operation.ID)]++
	}
	cursor := first.NextCursor
	for pages := 0; cursor != ""; pages++ {
		require.Less(t, pages, 10, "the listing does not end")
		next := page(cursor)
		for _, it := range next.Items {
			seen[fmt.Sprint(it.Operation.ID)]++
		}
		cursor = next.NextCursor
	}

	for id, n := range seen {
		assert.Equal(t, 1, n, "operation %s appeared %d times", id, n)
	}
	// 12 old operations exist, the 5 new ones are newer than the first page and are not returned.
	assert.Len(t, seen, before, "every operation that existed at the first page is listed exactly once")

	// A fresh listing sees all 17.
	total := 0
	for cursor, pages := "", 0; ; pages++ {
		require.Less(t, pages, 10)
		out := page(cursor)
		total += len(out.Items)
		if out.NextCursor == "" {
			break
		}
		cursor = out.NextCursor
	}
	assert.Equal(t, before+5, total)
}
