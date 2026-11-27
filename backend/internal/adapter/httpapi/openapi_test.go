package httpapi_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/audit"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/balance"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/node"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/role"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/settings"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
)

// The repository root is four levels above this package.
const repoRoot = "../../../.."

var methods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join(repoRoot, "api", "openapi.yaml"))
	require.NoError(t, err)
	return doc
}

func readFile(t *testing.T, rel ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{repoRoot}, rel...)...))
	require.NoError(t, err)
	return string(b)
}

// opKey is "METHOD /path" with the path as written in the specification.
type operation struct {
	key    string
	method string
	path   string
	op     *openapi3.Operation
}

func operations(doc *openapi3.T) map[string]operation {
	out := map[string]operation{}
	for p, item := range doc.Paths.Map() {
		for _, m := range methods {
			if op := item.GetOperation(m); op != nil {
				k := m + " " + p
				out[k] = operation{key: k, method: m, path: p, op: op}
			}
		}
	}
	return out
}

// specRow is one endpoint row of a table in docs/spec/03_api.md.
type specRow struct {
	key    string
	errors string // the "special errors" column, empty for tables without one
}

var rowEndpoint = regexp.MustCompile("^`(GET|POST|PUT|PATCH|DELETE) (/[^`?]*)(?:\\?[^`]*)?`$")

// specRows parses endpoint tables of section 5 and the health probes of section 1.
func specRows(t *testing.T) []specRow {
	t.Helper()
	text := readFile(t, "docs", "spec", "03_api.md")
	var rows []specRow
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "| "), "|")
		m := rowEndpoint.FindStringSubmatch(strings.TrimSpace(cells[0]))
		if m == nil {
			continue
		}
		r := specRow{key: m[1] + " " + m[2]}
		if len(cells) == 5 {
			r.errors = cells[4]
		}
		rows = append(rows, r)
	}
	for _, h := range []string{"GET /healthz", "GET /readyz"} {
		require.Contains(t, text, "`"+h+"`")
		rows = append(rows, specRow{key: h})
	}
	return rows
}

// Test-ID: T-CI-03
func TestOpenAPIValid(t *testing.T) {
	doc := loadSpec(t)
	require.NoError(t, doc.Validate(context.Background()))
	require.True(t, strings.HasPrefix(doc.OpenAPI, "3.0."), "OpenAPI version %q", doc.OpenAPI)
	require.NotEmpty(t, doc.Paths.Map())
}

// Test-ID: T-CI-03
func TestEveryEndpointDocumented(t *testing.T) {
	ops := operations(loadSpec(t))
	rows := specRows(t)
	require.GreaterOrEqual(t, len(rows), 33, "spec table parser found too few endpoints")

	want := map[string]bool{}
	for _, r := range rows {
		want[r.key] = true
		require.Contains(t, ops, r.key, "endpoint from 03_api.md is missing in openapi.yaml")
	}
	for k := range ops {
		require.True(t, want[k], "operation %s is not described in 03_api.md", k)
	}
}

var (
	statusOrCode = regexp.MustCompile("(\\d{3})|`([a-z_]+)`")
	errorTable   = regexp.MustCompile("^\\| ((?:`[a-z_]+`(?:, )?)+) \\| (\\d{3}) \\|")
	codeInSource = regexp.MustCompile(`Code = "([a-z_]+)"`)
)

// specErrorCodes parses the code table of 03_api.md section 2.3: code -> HTTP status.
func specErrorCodes(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, line := range strings.Split(readFile(t, "docs", "spec", "03_api.md"), "\n") {
		m := errorTable.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, c := range strings.Split(m[1], ", ") {
			out[strings.Trim(c, "`")] = m[2]
		}
	}
	return out
}

func apperrCodes(t *testing.T) map[string]bool {
	t.Helper()
	dir := filepath.Join(repoRoot, "backend", "internal", "domain", "apperr")
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	require.NoError(t, err)
	out := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range codeInSource.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = true
		}
	}
	require.NotEmpty(t, out)
	return out
}

func enumStrings(s *openapi3.Schema) []string {
	out := make([]string, 0, len(s.Enum))
	for _, e := range s.Enum {
		out = append(out, e.(string))
	}
	return out
}

// Test-ID: T-CI-03
func TestErrorCodeEnum(t *testing.T) {
	doc := loadSpec(t)
	enum := map[string]bool{}
	for _, c := range enumStrings(doc.Components.Schemas["ErrorCode"].Value) {
		enum[c] = true
	}
	table := specErrorCodes(t)
	require.GreaterOrEqual(t, len(table), 40)
	apperr := apperrCodes(t)

	for c := range table {
		require.True(t, enum[c], "code %q from 03_api.md section 2.3 is not in ErrorCode", c)
	}
	for c := range apperr {
		require.True(t, enum[c], "apperr code %q is not in ErrorCode", c)
	}
	// Codes that exist in neither the table nor apperr: invalid_password (03 section 5.1) is
	// the only one allowed; the transport codes of the table are not in apperr yet (M4.2).
	for c := range enum {
		if _, ok := table[c]; !ok && !apperr[c] {
			require.Equal(t, "invalid_password", c, "ErrorCode value %q is in neither 03_api.md nor apperr", c)
		}
	}

	// Per operation: every listed code is in the enum and answers with the status of the table.
	for k, o := range operations(doc) {
		raw, ok := o.op.Extensions["x-error-codes"].(map[string]any)
		if !ok {
			require.Contains(t, []string{"GET /healthz", "GET /readyz"}, k, "%s has no x-error-codes", k)
			continue
		}
		for status, list := range raw {
			require.NotNil(t, o.op.Responses.Value(status), "%s lists codes for %s but has no such response", k, status)
			for _, c := range list.([]any) {
				code := c.(string)
				require.True(t, enum[code], "%s: unknown code %q", k, code)
				if want, ok := table[code]; ok {
					require.Equal(t, want, status, "%s: code %q must be answered with %s", k, code, want)
				}
			}
		}
	}
}

// Test-ID: T-CI-03
func TestErrorResponses(t *testing.T) {
	doc := loadSpec(t)
	ops := operations(doc)
	health := []string{"GET /healthz", "GET /readyz"}

	for k, o := range ops {
		if slices.Contains(health, k) {
			continue
		}
		resp := o.op.Responses
		for status, r := range resp.Map() {
			n, err := strconv.Atoi(status)
			require.NoError(t, err, "%s: non-numeric status %q", k, status)
			if n < 400 {
				continue
			}
			mt := r.Value.Content.Get("application/problem+json")
			require.NotNil(t, mt, "%s %s: error responses are application/problem+json", k, status)
			require.Equal(t, "#/components/schemas/Problem", mt.Schema.Ref, "%s %s", k, status)
		}
		for _, s := range []string{"429", "500", "503"} {
			require.NotNil(t, resp.Value(s), "%s: missing %s", k, s)
		}
		if o.op.Security == nil || len(*o.op.Security) > 0 {
			require.NotNil(t, resp.Value("401"), "%s: authenticated operation without 401", k)
		}
		if strings.Contains(o.path, "{") {
			require.NotNil(t, resp.Value("400"), "%s: missing 400", k)
			require.NotNil(t, resp.Value("404"), "%s: missing 404", k)
		}
		if o.op.RequestBody != nil {
			for _, s := range []string{"400", "413", "415"} {
				require.NotNil(t, resp.Value(s), "%s: body operation without %s", k, s)
			}
		}
	}

	// Error codes and statuses named in the "special errors" column of 03_api.md.
	for _, r := range specRows(t) {
		if r.errors == "" {
			continue
		}
		o := ops[r.key]
		raw, _ := o.op.Extensions["x-error-codes"].(map[string]any)
		status := ""
		for _, m := range statusOrCode.FindAllStringSubmatch(r.errors, -1) {
			if m[1] != "" {
				status = m[1]
				require.NotNil(t, o.op.Responses.Value(status), "%s: 03_api.md names %s", r.key, status)
				continue
			}
			require.NotEmpty(t, status, "%s: code %q without status in 03_api.md", r.key, m[2])
			list, _ := raw[status].([]any)
			require.Contains(t, list, any(m[2]), "%s: code %q at %s from 03_api.md", r.key, m[2], status)
		}
	}
}

// Test-ID: T-CI-03
func TestSecurityOfOperations(t *testing.T) {
	doc := loadSpec(t)

	bearer := doc.Components.SecuritySchemes["bearerAuth"].Value
	require.Equal(t, "http", bearer.Type)
	require.Equal(t, "bearer", bearer.Scheme)
	require.Equal(t, "JWT", bearer.BearerFormat)
	cookie := doc.Components.SecuritySchemes["refreshCookie"].Value
	require.Equal(t, "apiKey", cookie.Type)
	require.Equal(t, "cookie", cookie.In)
	require.Equal(t, "tb_refresh", cookie.Name)
	csrf := doc.Components.SecuritySchemes["csrfHeader"].Value
	require.Equal(t, "apiKey", csrf.Type)
	require.Equal(t, "header", csrf.In)
	require.Equal(t, "X-TB-CSRF", csrf.Name)

	public := []string{"GET /healthz", "GET /readyz", "POST /auth/login"}
	var gotPublic []string
	for k, o := range operations(doc) {
		sec := doc.Security
		if o.op.Security != nil {
			sec = *o.op.Security
		}
		switch {
		case len(sec) == 0:
			gotPublic = append(gotPublic, k)
		case k == "POST /auth/refresh":
			require.Len(t, sec, 1, k)
			require.ElementsMatch(t, []string{"refreshCookie", "csrfHeader"}, keys(sec[0]), k)
		default:
			require.Len(t, sec, 1, "%s: exactly one requirement", k)
			require.Equal(t, []string{"bearerAuth"}, keys(sec[0]), "%s must require the bearer token", k)
		}
	}
	sort.Strings(gotPublic)
	sort.Strings(public)
	require.Equal(t, public, gotPublic, "the list of operations without security must stay explicit and short")
}

func keys(r openapi3.SecurityRequirement) []string {
	out := make([]string, 0, len(r))
	for k := range r {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Test-ID: T-CI-03
func TestIdempotencyKeyHeader(t *testing.T) {
	ops := operations(loadSpec(t))
	with := []string{"POST /nodes/{id}/credits", "POST /nodes/{id}/debits", "POST /transfers"}
	for k, o := range ops {
		found := false
		for _, p := range o.op.Parameters {
			if p.Value.In == "header" && p.Value.Name == "Idempotency-Key" {
				found = true
				require.True(t, p.Value.Required)
				require.Equal(t, uint64(1), p.Value.Schema.Value.MinLength)
				require.NotNil(t, p.Value.Schema.Value.MaxLength)
				require.Equal(t, uint64(128), *p.Value.Schema.Value.MaxLength)
			}
		}
		require.Equal(t, slices.Contains(with, k), found, "%s: Idempotency-Key", k)
	}
}

var (
	moneyName = regexp.MustCompile(`(?i)(amount|balance|balanceAfter|credits|debits|net)$`)
	intNames  = []string{"expiresIn", "status", "childrenCount", "count", "limit", "RetryAfter"}
)

// walk visits every schema reachable from s once per path, with the property name that led to it.
func walk(s *openapi3.SchemaRef, name string, seen map[*openapi3.Schema]bool, f func(name string, s *openapi3.Schema)) {
	if s == nil || s.Value == nil || seen[s.Value] {
		return
	}
	seen[s.Value] = true
	defer delete(seen, s.Value) // only the current path guards against cycles: shared schemas are visited per field name
	f(name, s.Value)
	for n, p := range s.Value.Properties {
		walk(p, n, seen, f)
	}
	walk(s.Value.Items, name, seen, f)
	for _, group := range []openapi3.SchemaRefs{s.Value.AllOf, s.Value.OneOf, s.Value.AnyOf} {
		for _, g := range group {
			walk(g, name, seen, f)
		}
	}
	if ap := s.Value.AdditionalProperties.Schema; ap != nil {
		walk(ap, name, seen, f)
	}
}

// Test-ID: T-CI-03
func TestMoneyIsStringWithPattern(t *testing.T) {
	doc := loadSpec(t)
	seen := map[*openapi3.Schema]bool{}
	moneyFields := 0

	visit := func(name string, s *openapi3.Schema) {
		require.False(t, s.Type.Includes("number"), "%s: number is not allowed anywhere in the contract", name)
		if s.Type.Is("integer") {
			require.Contains(t, intNames, name, "integer %q is not a known non-money field", name)
		}
		if name != "" && moneyName.MatchString(name) {
			moneyFields++
			require.True(t, s.Type.Is("string"), "%s must be a string", name)
			require.NotEmpty(t, s.Pattern, "%s must have a pattern", name)
			re := regexp.MustCompile(s.Pattern)
			for _, ok := range []string{"0", "7", "1050", strconv.FormatInt(1<<63-1, 10)} {
				require.Regexp(t, re, ok, "%s accepts %q", name, ok)
			}
			for _, bad := range []string{"", "1.5", "01", "1e3", "abc", " 1", "1 ", "+1"} {
				require.NotRegexp(t, re, bad, "%s rejects %q", name, bad)
			}
		}
	}
	for n, s := range doc.Components.Schemas {
		walk(s, "", seen, visit)
		_ = n
	}
	for _, p := range doc.Components.Parameters {
		walk(p.Value.Schema, p.Value.Name, seen, visit)
	}
	for n, h := range doc.Components.Headers {
		walk(h.Value.Schema, n, seen, visit)
	}
	require.GreaterOrEqual(t, moneyFields, 8, "money field detection found too few fields")

	// Non-negative amounts reject a sign; report sums accept it.
	nonNegative := regexp.MustCompile(doc.Components.Schemas["MoneyAmount"].Value.Pattern)
	require.NotRegexp(t, nonNegative, "-1")
	require.NotRegexp(t, nonNegative, "10000000000000000000")
	signed := regexp.MustCompile(doc.Components.Schemas["ReportSum"].Value.Pattern)
	require.Regexp(t, signed, "-1050")

	// The balance of an operation and the transfer amount are the non-negative type.
	for _, c := range []struct{ schema, field string }{
		{"Operation", "amount"},
		{"Operation", "balanceAfter"},
		{"Balance", "amount"},
		{"OperationRequest", "amount"},
		{"TransferRequest", "amount"},
	} {
		require.Equal(t, "#/components/schemas/MoneyAmount",
			doc.Components.Schemas[c.schema].Value.Properties[c.field].Ref, "%s.%s", c.schema, c.field)
	}
}

// Test-ID: T-CI-03
func TestFormats(t *testing.T) {
	doc := loadSpec(t)
	seen := map[*openapi3.Schema]bool{}
	idName := regexp.MustCompile(`(?i)(^id|Id|ids)$`)
	timeName := regexp.MustCompile(`(At|periodStart)$`)
	visit := func(name string, s *openapi3.Schema) {
		switch {
		case name == "requestId" || name == "targetId":
			// request ids are opaque strings; the audit target id depends on the target type
		case name != "" && idName.MatchString(name) && s.Type.Is("string"):
			require.Equal(t, "uuid", s.Format, "%s must be a uuid", name)
		case timeName.MatchString(name) && s.Type.Is("string"):
			want := "date-time"
			if name == "periodStart" {
				want = "date"
			}
			require.Equal(t, want, s.Format, "%s", name)
		}
	}
	for _, s := range doc.Components.Schemas {
		walk(s, "", seen, visit)
	}
	for _, p := range doc.Components.Parameters {
		if (p.Value.In == "path" && p.Value.Name != "key") || strings.HasSuffix(p.Value.Name, "Id") {
			require.Equal(t, "uuid", p.Value.Schema.Value.Format, p.Value.Name)
		}
	}
}

// Test-ID: T-CI-03
func TestPagination(t *testing.T) {
	doc := loadSpec(t)
	limit := doc.Components.Parameters["Limit"].Value.Schema.Value
	require.Equal(t, float64(1), *limit.Min)
	require.Equal(t, float64(200), *limit.Max)
	require.EqualValues(t, 50, limit.Default)

	pages := 0
	for name, s := range doc.Components.Schemas {
		if !strings.HasSuffix(name, "Page") {
			continue
		}
		pages++
		require.ElementsMatch(t, []string{"items", "nextCursor"}, s.Value.Required, name)
		require.True(t, s.Value.Properties["nextCursor"].Value.Nullable, name)
	}
	require.Equal(t, 5, pages)

	listing := map[string]bool{
		"GET /nodes/{id}/children": true, "GET /nodes/{id}/move-targets": true, "GET /nodes/{id}/members": true,
		"GET /operations": true, "GET /nodes/{id}/reports/children": true, "GET /audit": true,
	}
	for k, o := range operations(doc) {
		var names []string
		for _, p := range o.op.Parameters {
			names = append(names, p.Value.Name)
		}
		require.Equal(t, listing[k], slices.Contains(names, "limit") && slices.Contains(names, "cursor"), k)
	}
}

// Test-ID: T-CI-03
func TestDomainEnumsMatch(t *testing.T) {
	doc := loadSpec(t)
	enum := func(name string) []string { return enumStrings(doc.Components.Schemas[name].Value) }

	var nodeTypes []string
	for _, ty := range []node.Type{node.TypePlatform, node.TypePartner, node.TypeCompany, node.TypeUnit} {
		nodeTypes = append(nodeTypes, string(ty))
	}
	require.ElementsMatch(t, nodeTypes, enum("NodeType"))
	require.ElementsMatch(t, []string{string(node.StatusActive), string(node.StatusInactive)}, enum("NodeStatus"))

	var roles, perms, kinds, keys, actions []string
	for _, r := range role.All() {
		roles = append(roles, string(r))
	}
	for _, p := range role.AllPermissions() {
		perms = append(perms, string(p))
	}
	for _, k := range balance.Kinds() {
		kinds = append(kinds, string(k))
	}
	for _, k := range settings.Keys() {
		keys = append(keys, string(k))
	}
	for _, a := range audit.Actions() {
		actions = append(actions, string(a))
	}
	require.ElementsMatch(t, roles, enum("Role"))
	require.ElementsMatch(t, perms, enum("Permission"))
	require.ElementsMatch(t, kinds, enum("OperationKind"))
	require.ElementsMatch(t, keys, enum("SettingKey"))
	require.ElementsMatch(t, actions, enum("AuditAction"))
	targets := []string{
		string(audit.TargetNode), string(audit.TargetMember), string(audit.TargetUser),
		string(audit.TargetSetting), string(audit.TargetBalance),
	}
	require.ElementsMatch(t, targets, enum("AuditTargetType"))

	// Length limits: the boundary accepted by the domain equals the one in the schema.
	name := doc.Components.Schemas["NodeName"].Value
	require.EqualValues(t, 1, name.MinLength)
	maxLen := int(*name.MaxLength)
	_, err := node.ParseName(strings.Repeat("я", maxLen))
	require.NoError(t, err)
	_, err = node.ParseName(strings.Repeat("я", maxLen+1))
	require.Error(t, err)
	_, err = node.ParseName("")
	require.Error(t, err)

	pw := doc.Components.Schemas["Password"].Value
	require.NoError(t, user.ValidatePassword(strings.Repeat("a", int(pw.MinLength))))
	require.Error(t, user.ValidatePassword(strings.Repeat("a", int(pw.MinLength)-1)))
	require.NoError(t, user.ValidatePassword(strings.Repeat("a", int(*pw.MaxLength))))
	require.Error(t, user.ValidatePassword(strings.Repeat("a", int(*pw.MaxLength)+1)))

	em := doc.Components.Schemas["Email"].Value
	require.EqualValues(t, 3, em.MinLength)
	require.EqualValues(t, 254, *em.MaxLength)

	comment := doc.Components.Schemas["OperationRequest"].Value.Properties["comment"].Value
	require.EqualValues(t, balance.MaxCommentRunes, *comment.MaxLength)
	require.EqualValues(t, node.MaxChildren, *doc.Components.Schemas["Node"].Value.Properties["childrenCount"].Value.Max)
}
