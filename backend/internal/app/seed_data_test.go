package app_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/app"
	"github.com/Gwakko/tenant-backoffice/backend/seed"
)

func loadDemo(t *testing.T) app.SeedData {
	t.Helper()
	d, err := app.LoadSeedData(seed.Demo)
	require.NoError(t, err)
	return d
}

// The demo file is a valid document with the shape the milestone asks for.
func TestDemoDataShape(t *testing.T) {
	d := loadDemo(t)

	typeOf := map[uuid.UUID]string{}
	parentOf := map[uuid.UUID]uuid.UUID{}
	count := map[string]int{}
	for _, n := range d.Nodes {
		typeOf[n.ID] = n.Type
		count[n.Type]++
		if n.Parent != nil {
			parentOf[n.ID] = *n.Parent
		}
	}
	require.Equal(t, 1, count["platform"])
	require.GreaterOrEqual(t, count["partner"], 2)
	require.GreaterOrEqual(t, count["company"], 4)
	require.GreaterOrEqual(t, count["unit"], 12)
	perParent := map[uuid.UUID]int{}
	for _, n := range d.Nodes {
		if n.Parent != nil {
			perParent[*n.Parent]++
		}
	}
	for id, typ := range typeOf {
		switch typ {
		case "partner":
			require.GreaterOrEqual(t, perParent[id], 2, "partner %s needs two companies", id)
		case "company":
			require.GreaterOrEqual(t, perParent[id], 3, "company %s needs three units", id)
		}
	}

	roles := map[string]bool{}
	branches := map[uuid.UUID]map[uuid.UUID]bool{} // user -> partners reached
	for _, r := range d.Roles {
		roles[r.Role] = true
		n := r.Node
		for typeOf[n] != "partner" && typeOf[n] != "platform" {
			n = parentOf[n]
		}
		if branches[r.User] == nil {
			branches[r.User] = map[uuid.UUID]bool{}
		}
		branches[r.User][n] = true
	}
	for _, want := range []string{"owner", "admin", "analyst", "viewer"} {
		require.True(t, roles[want], "role %s is missing", want)
	}
	var multi, inactive bool
	for _, u := range d.Users {
		if u.Email == "multi@demo.test" {
			multi = len(branches[u.ID]) >= 2
		}
		if u.Status == "inactive" {
			inactive = true
		}
	}
	require.True(t, multi, "user multi needs roles in two unrelated branches")
	require.True(t, inactive, "an inactive user is required")

	var off bool
	levels := map[string]bool{}
	for _, s := range d.Settings {
		levels[typeOf[s.Node]] = true
		if s.Key == "operations_enabled" && string(s.Value) == "false" {
			off = true
		}
	}
	require.True(t, off, "one node must forbid operations")
	require.GreaterOrEqual(t, len(levels), 3, "settings on at least three levels")

	kinds := map[string]bool{}
	minDay := 0
	for _, o := range d.Operations {
		kinds[o.Kind] = true
		minDay = min(minDay, o.Day)
	}
	require.True(t, kinds["credit"] && kinds["debit"])
	require.NotEmpty(t, d.Transfers)
	require.LessOrEqual(t, minDay, -59, "history must span about 60 days")
}

// Plain passwords live only in demo.json, and there only as demo values.
func TestDemoPasswordsAreDemoValues(t *testing.T) {
	d := loadDemo(t)
	require.Contains(t, d.Comment, "not a real credential")
	for _, u := range d.Users {
		require.True(t, strings.HasPrefix(u.Password, "demo-"), "user %s", u.ID)
		require.True(t, strings.HasSuffix(u.Email, "@demo.test"))
	}
}

func TestSeedDataRejectsBadDocuments(t *testing.T) {
	mutate := func(f func(m map[string]any)) []byte {
		var m map[string]any
		require.NoError(t, json.Unmarshal(seed.Demo, &m))
		f(m)
		b, err := json.Marshal(m)
		require.NoError(t, err)
		return b
	}
	first := func(m map[string]any, key string) map[string]any {
		return m[key].([]any)[0].(map[string]any)
	}
	cases := map[string][]byte{
		"unknown field": mutate(func(m map[string]any) { m["extra"] = 1 }),
		"unknown parent": mutate(func(m map[string]any) {
			first(m, "nodes")["parent"] = "ffffffff-0000-4000-8000-000000000000"
		}),
		"short password": mutate(func(m map[string]any) { first(m, "users")["password"] = "short" }),
		"zero amount":    mutate(func(m map[string]any) { first(m, "operations")["amount"] = 0 }),
		"future day":     mutate(func(m map[string]any) { first(m, "operations")["day"] = 0 }),
		"unknown setting key": mutate(func(m map[string]any) {
			first(m, "settings")["key"] = "foo"
		}),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := app.LoadSeedData(raw)
			require.Error(t, err)
		})
	}
}
