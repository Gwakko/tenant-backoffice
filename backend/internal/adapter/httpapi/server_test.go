package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
)

var testActor = user.ID(uuid.MustParse("0190f3a0-0000-7000-8000-0000000000aa"))

// fakeAuth accepts the token "good-token" and fails the others like the use case.
type fakeAuth struct{}

func (fakeAuth) Execute(_ context.Context, in auth.AuthenticateInput) (auth.AuthenticateOutput, error) {
	switch in.AccessToken {
	case "good-token":
		return auth.AuthenticateOutput{Actor: testActor}, nil
	case "expired-token":
		return auth.AuthenticateOutput{}, apperr.TokenExpiredError()
	case "db-down-token":
		return auth.AuthenticateOutput{}, apperr.UnavailableError()
	default:
		return auth.AuthenticateOutput{}, apperr.TokenInvalidError()
	}
}

// fakeLimiter allows everything unless deny is set.
type fakeLimiter struct {
	mu    sync.Mutex
	deny  bool
	retry time.Duration
	keys  []string
}

func (l *fakeLimiter) Allow(key string, _ int, _ time.Duration) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys = append(l.keys, key)
	if l.deny {
		return false, l.retry
	}
	return true, 0
}

// syncBuffer is a log sink safe for concurrent use.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fakeHandlers implements the operations the tests need; calling another
// implemented operation panics on the nil embedded interface.
type fakeHandlers struct {
	gen.StrictServerInterface
	getNode func() error
	login   func() error
	getMe   func() error
}

func (f fakeHandlers) GetNode(context.Context, gen.GetNodeRequestObject) (gen.GetNodeResponseObject, error) {
	return nil, f.getNode()
}

func (f fakeHandlers) Login(context.Context, gen.LoginRequestObject) (gen.LoginResponseObject, error) {
	return nil, f.login()
}

func (f fakeHandlers) GetMe(context.Context, gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	return nil, f.getMe()
}

type harness struct {
	h       http.Handler
	log     *syncBuffer
	limiter *fakeLimiter
	clock   *testClock
}

func newHarness(t *testing.T, handlers gen.StrictServerInterface, implemented ...string) *harness {
	t.Helper()
	buf := &syncBuffer{}
	clk := newTestClock()
	lim := &fakeLimiter{}
	impl := map[string]bool{}
	for _, op := range implemented {
		impl[op] = true
	}
	h, err := httpapi.NewServer(httpapi.Options{
		Log:           slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Clock:         clk,
		Authenticator: fakeAuth{},
		Limiter:       lim,
		Health:        httpapi.NewHealthHandler(func(context.Context) error { return nil }),
		Handlers:      handlers,
		Implemented:   impl,
		MaxBodyBytes:  64 << 10,
		Rates:         httpapi.DefaultRateBudgets(),
		NewRequestID:  func() string { return "generated-id-0001" },
	})
	require.NoError(t, err)
	return &harness{h: h, log: buf, limiter: lim, clock: clk}
}

type result struct {
	*httptest.ResponseRecorder
}

func (r result) problem(t *testing.T) httpapi.Problem {
	t.Helper()
	require.Equal(t, "application/problem+json", r.Header().Get("Content-Type"))
	var p httpapi.Problem
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &p))
	require.Equal(t, r.Code, p.Status)
	return p
}

type req struct {
	method, path, body string
	header             map[string]string
}

func (h *harness) do(t *testing.T, rq req) result {
	t.Helper()
	var body io.Reader
	if rq.body != "" {
		body = strings.NewReader(rq.body)
	}
	r := httptest.NewRequestWithContext(context.Background(), rq.method, rq.path, body)
	r.RemoteAddr = "192.0.2.10:5555"
	for k, v := range rq.header {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, r)
	return result{rec}
}

func authed(extra map[string]string) map[string]string {
	m := map[string]string{"Authorization": "Bearer good-token"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

var jsonHeader = map[string]string{"Content-Type": "application/json"}

func withJSON(extra map[string]string) map[string]string {
	m := authed(jsonHeader)
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestHealthAndPlaceholder(t *testing.T) {
	h := newHarness(t, nil)
	require.Equal(t, http.StatusOK, h.do(t, req{method: "GET", path: "/healthz"}).Code)
	require.Equal(t, http.StatusOK, h.do(t, req{method: "GET", path: "/readyz"}).Code)

	res := h.do(t, req{method: "GET", path: "/api/v1/me", header: authed(nil)})
	p := res.problem(t)
	require.Equal(t, http.StatusNotImplemented, res.Code)
	require.Equal(t, "not_implemented", p.Code)

	// Unknown paths: the same problem body, no routing details.
	res = h.do(t, req{method: "GET", path: "/api/v1/nope"})
	require.Equal(t, http.StatusNotFound, res.Code)
	require.Equal(t, "not_found", res.problem(t).Code)
}

func TestRequestID(t *testing.T) {
	h := newHarness(t, nil)
	for _, valid := range []string{"abcdefgh", "0190f3a0-0000-7000-8000-0000000000aa", strings.Repeat("a", 64)} {
		res := h.do(t, req{method: "GET", path: "/healthz", header: map[string]string{"X-Request-Id": valid}})
		require.Equal(t, valid, res.Header().Get("X-Request-Id"))
	}
	for _, bad := range []string{"short", strings.Repeat("a", 65), "has space 123", "evil\"<script>", "id_with_underscore"} {
		res := h.do(t, req{method: "GET", path: "/healthz", header: map[string]string{"X-Request-Id": bad}})
		require.Equal(t, "generated-id-0001", res.Header().Get("X-Request-Id"), bad)
	}
	res := h.do(t, req{method: "GET", path: "/api/v1/me"})
	require.Equal(t, "generated-id-0001", res.problem(t).RequestID, "an error answer carries the request id")
}

// Test-ID: T-U-66
func TestPanicDoesNotLeak(t *testing.T) {
	h := newHarness(t, fakeHandlers{getMe: func() error { panic("secret-panic-detail: password=hunter2") }}, "GetMe")
	res := h.do(t, req{method: "GET", path: "/api/v1/me", header: authed(map[string]string{"X-Request-Id": "req-panic-0001"})})
	p := res.problem(t)
	require.Equal(t, http.StatusInternalServerError, res.Code)
	require.Equal(t, "internal", p.Code)
	require.Equal(t, "req-panic-0001", p.RequestID)
	require.NotContains(t, res.Body.String(), "secret-panic-detail")
	require.NotContains(t, res.Body.String(), "hunter2")
	require.NotContains(t, res.Body.String(), "goroutine")
	require.Contains(t, h.log.String(), "secret-panic-detail", "the detail goes to the log")
	require.Contains(t, h.log.String(), "req-panic-0001")

	// A nil dereference inside an implemented handler is a panic as well.
	h = newHarness(t, fakeHandlers{}, "GetNode")
	res = h.do(t, req{method: "GET", path: "/api/v1/nodes/" + nodeA, header: authed(nil)})
	require.Equal(t, http.StatusInternalServerError, res.Code)
	require.Equal(t, "internal", res.problem(t).Code)
	require.NotContains(t, res.Body.String(), "nil pointer")
}

func TestInternalErrorDoesNotLeak(t *testing.T) {
	raw := `ERROR: relation "tb_secret_table" does not exist (SQLSTATE 42P01) host=db.internal password=s3cr3t`
	h := newHarness(t, fakeHandlers{getNode: func() error { return errors.New(raw) }}, "GetNode")
	res := h.do(t, req{method: "GET", path: "/api/v1/nodes/" + nodeA, header: authed(nil)})
	p := res.problem(t)
	require.Equal(t, http.StatusInternalServerError, res.Code)
	require.Equal(t, "internal", p.Code)
	for _, leak := range []string{"tb_secret_table", "SQLSTATE", "s3cr3t", "db.internal"} {
		require.NotContains(t, res.Body.String(), leak)
	}
	require.Contains(t, h.log.String(), "tb_secret_table", "the cause is logged")
	require.Contains(t, h.log.String(), p.RequestID)

	// An apperr with an unmapped code is internal too.
	h = newHarness(t, fakeHandlers{getNode: func() error { return apperr.New("made_up_code", "details here") }}, "GetNode")
	res = h.do(t, req{method: "GET", path: "/api/v1/nodes/" + nodeA, header: authed(nil)})
	require.Equal(t, "internal", res.problem(t).Code)
	require.NotContains(t, res.Body.String(), "details here")
	require.NotContains(t, res.Body.String(), "made_up_code")
}

var requestIDInBody = regexp.MustCompile(`"requestId":"[^"]*"`)

func withoutRequestID(body string) string {
	return requestIDInBody.ReplaceAllString(body, `"requestId":""`)
}

func TestNotFoundBodiesAreIdentical(t *testing.T) {
	// A missing object and an object outside the actor's subtrees: the use cases
	// may word their errors differently, the answer must not.
	missing := apperr.NotFoundError("node")
	foreign := apperr.New(apperr.NotFound, "node "+nodeB+" is outside the subtree (out_of_scope)")
	var next error
	h := newHarness(t, fakeHandlers{getNode: func() error { return next }}, "GetNode")

	next = missing
	a := h.do(t, req{method: "GET", path: "/api/v1/nodes/" + nodeA, header: authed(map[string]string{"X-Request-Id": "request-aaaa"})})
	next = foreign
	b := h.do(t, req{method: "GET", path: "/api/v1/nodes/" + nodeB, header: authed(map[string]string{"X-Request-Id": "request-bbbb"})})
	next = fmtWrap(foreign)
	c := h.do(t, req{method: "GET", path: "/api/v1/nodes/" + nodeB, header: authed(map[string]string{"X-Request-Id": "request-cccc"})})

	require.Equal(t, http.StatusNotFound, a.Code)
	require.Equal(t, a.Code, b.Code)
	require.Equal(t, withoutRequestID(a.Body.String()), withoutRequestID(b.Body.String()))
	require.Equal(t, withoutRequestID(a.Body.String()), withoutRequestID(c.Body.String()))
	require.NotContains(t, b.Body.String(), nodeB)
	require.NotContains(t, b.Body.String(), "out_of_scope")
	require.Equal(t, a.Header().Get("Content-Type"), b.Header().Get("Content-Type"))
	require.Equal(t, "request-aaaa", a.problem(t).RequestID)
	require.Equal(t, "request-bbbb", b.problem(t).RequestID)
}

func fmtWrap(err error) error { return errors.Join(errors.New("repo"), err) }

func TestLoginFailuresAreIdentical(t *testing.T) {
	var next error
	h := newHarness(t, fakeHandlers{login: func() error { return next }}, "Login")
	body := `{"email":"a@example.com","password":"x"}`
	var bodies []string
	for _, e := range []error{
		apperr.New(apperr.InvalidCredentials, "wrong password for user "+nodeA),
		apperr.New(apperr.InvalidCredentials, "unknown email"),
		apperr.InvalidCredentialsError(),
	} {
		next = e
		res := h.do(t, req{method: "POST", path: "/api/v1/auth/login", body: body, header: jsonHeader})
		require.Equal(t, http.StatusUnauthorized, res.Code)
		bodies = append(bodies, withoutRequestID(res.Body.String()))
	}
	require.Equal(t, bodies[0], bodies[1])
	require.Equal(t, bodies[0], bodies[2])
	require.Contains(t, bodies[0], `"code":"invalid_credentials"`)
}

func TestErrorMappingTable(t *testing.T) {
	spec := specErrorCodes(t)
	for code, status := range spec {
		got, ok := httpapi.StatusOf(apperr.Code(code))
		require.True(t, ok, "code %s of 03 section 2.3 has no mapping", code)
		require.Equal(t, status, strconv.Itoa(got), code)
	}
	for code := range apperrCodes(t) {
		got, ok := httpapi.StatusOf(apperr.Code(code))
		require.True(t, ok, "apperr code %s has no mapping", code)
		if want, inSpec := spec[code]; inSpec {
			require.Equal(t, want, strconv.Itoa(got), code)
		}
	}
}

func TestProblemShapeMatchesContract(t *testing.T) {
	h := newHarness(t, nil)
	res := h.do(t, req{method: "POST", path: "/api/v1/nodes/" + nodeA + "/credits", header: withJSON(nil), body: `{"amount":"1"}`})
	require.Equal(t, http.StatusBadRequest, res.Code)
	dec := json.NewDecoder(res.Body)
	dec.DisallowUnknownFields()
	var p gen.Problem
	require.NoError(t, dec.Decode(&p))
	require.Equal(t, gen.ErrorCodeIdempotencyKeyRequired, p.Code)
	require.Equal(t, "https://tenant-backoffice.invalid/problems/idempotency_key_required", p.Type)
	require.NotEmpty(t, p.RequestId)
	require.NotNil(t, p.Detail)
}

func TestAuthnErrors(t *testing.T) {
	h := newHarness(t, fakeHandlers{getMe: func() error { return errors.New("reached") }}, "GetMe")
	cases := []struct {
		name   string
		header map[string]string
		status int
		code   string
	}{
		{"no header", nil, 401, "token_missing"},
		{"basic scheme", map[string]string{"Authorization": "Basic abc"}, 401, "token_invalid"},
		{"bearer without token", map[string]string{"Authorization": "Bearer"}, 401, "token_invalid"},
		{"bearer with spaces", map[string]string{"Authorization": "Bearer a b"}, 401, "token_invalid"},
		{"bad token", map[string]string{"Authorization": "Bearer nope"}, 401, "token_invalid"},
		{"expired", map[string]string{"Authorization": "Bearer expired-token"}, 401, "token_expired"},
		{"store down", map[string]string{"Authorization": "Bearer db-down-token"}, 503, "db_unavailable"},
		{"lower-case scheme", map[string]string{"Authorization": "bearer good-token"}, 500, "internal"}, // passes authn, handler errors
	}
	for _, c := range cases {
		res := h.do(t, req{method: "GET", path: "/api/v1/me", header: c.header})
		p := res.problem(t)
		require.Equal(t, c.status, res.Code, c.name)
		require.Equal(t, c.code, p.Code, c.name)
		if c.status == 401 {
			require.Equal(t, "Bearer", res.Header().Get("WWW-Authenticate"), c.name)
		}
	}
	// Login and refresh are public, health too.
	res := h.do(t, req{method: "POST", path: "/api/v1/auth/refresh"})
	require.NotEqual(t, http.StatusUnauthorized, res.Code)
}

func TestBodyLimitAndContentType(t *testing.T) {
	h := newHarness(t, nil)
	big := `{"name":"` + strings.Repeat("x", 64<<10) + `"}`
	res := h.do(t, req{method: "PATCH", path: "/api/v1/nodes/" + nodeA, body: big, header: withJSON(nil)})
	require.Equal(t, http.StatusRequestEntityTooLarge, res.Code)
	require.Equal(t, "payload_too_large", res.problem(t).Code)

	// Exactly at the limit is accepted by the limit (it fails later, not with 413).
	atLimit := `{"name":"` + strings.Repeat("x", 64<<10-len(`{"name":""}`)) + `"}`
	require.Len(t, atLimit, 64<<10)
	res = h.do(t, req{method: "PATCH", path: "/api/v1/nodes/" + nodeA, body: atLimit, header: withJSON(nil)})
	require.NotEqual(t, http.StatusRequestEntityTooLarge, res.Code)

	// The body is refused before authentication costs anything.
	res = h.do(t, req{method: "PATCH", path: "/api/v1/nodes/" + nodeA, body: big, header: jsonHeader})
	require.Equal(t, http.StatusRequestEntityTooLarge, res.Code)

	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "application/jsonx", "application/json; charset=latin1"} {
		res = h.do(t, req{method: "PATCH", path: "/api/v1/nodes/" + nodeA, body: `{"name":"x"}`, header: authed(map[string]string{"Content-Type": ct})})
		require.Equal(t, http.StatusUnsupportedMediaType, res.Code, ct)
		require.Equal(t, "unsupported_media_type", res.problem(t).Code)
	}
	for _, ct := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON"} {
		res = h.do(t, req{method: "PATCH", path: "/api/v1/nodes/" + nodeA, body: `{"name":"x"}`, header: authed(map[string]string{"Content-Type": ct})})
		require.Equal(t, http.StatusNotImplemented, res.Code, ct)
	}
	// A request without a body needs no content type.
	res = h.do(t, req{method: "GET", path: "/api/v1/nodes/" + nodeA, header: authed(nil)})
	require.Equal(t, http.StatusNotImplemented, res.Code)
}

func TestBodyShape(t *testing.T) {
	h := newHarness(t, nil)
	post := func(body string) httpapi.Problem {
		res := h.do(t, req{method: "POST", path: "/api/v1/nodes/" + nodeA + "/children", body: body, header: withJSON(nil)})
		require.Equal(t, http.StatusBadRequest, res.Code, body)
		return res.problem(t)
	}
	p := post(`{"name":"x","type":"company","admin":true}`)
	require.Equal(t, "validation_failed", p.Code)
	require.Equal(t, "admin", p.Errors[0].Field)
	require.Equal(t, "validation_failed", post(`{"name":`).Code)
	require.Equal(t, "validation_failed", post(`{"name":"x"} {"name":"y"}`).Code)
	require.Equal(t, "validation_failed", post(`{"name":5}`).Code)
	require.Equal(t, "validation_failed", post(`[]`).Code)

	// A JSON number in the amount (EC-76).
	res := h.do(t, req{
		method: "POST", path: "/api/v1/nodes/" + nodeA + "/credits", body: `{"amount":100}`,
		header: withJSON(map[string]string{"Idempotency-Key": "k-1"}),
	})
	require.Equal(t, http.StatusBadRequest, res.Code)
	require.Equal(t, "invalid_amount", res.problem(t).Code)

	// An empty body where one is required.
	res = h.do(t, req{method: "POST", path: "/api/v1/nodes/" + nodeA + "/children", header: withJSON(nil)})
	require.Equal(t, "validation_failed", res.problem(t).Code)

	// A valid body reaches the (placeholder) handler.
	res = h.do(t, req{method: "POST", path: "/api/v1/nodes/" + nodeA + "/children", body: `{"name":"x","type":"company"}`, header: withJSON(nil)})
	require.Equal(t, http.StatusNotImplemented, res.Code)
}

func TestInvalidIDAndIdempotencyHeader(t *testing.T) {
	h := newHarness(t, nil)
	res := h.do(t, req{method: "GET", path: "/api/v1/nodes/123", header: authed(nil)})
	require.Equal(t, http.StatusBadRequest, res.Code)
	require.Equal(t, "invalid_id", res.problem(t).Code)
	res = h.do(t, req{method: "GET", path: "/api/v1/nodes/%2e%2e%2fetc", header: authed(nil)})
	require.Equal(t, "invalid_id", res.problem(t).Code)

	for name, key := range map[string]string{"": "idempotency_key_required", "bad key!": "idempotency_key_invalid", strings.Repeat("a", 129): "idempotency_key_invalid"} {
		hdr := withJSON(nil)
		if name != "" {
			hdr["Idempotency-Key"] = name
		}
		res = h.do(t, req{method: "POST", path: "/api/v1/transfers", body: `{"amount":"1"}`, header: hdr})
		require.Equal(t, http.StatusBadRequest, res.Code)
		require.Equal(t, key, res.problem(t).Code)
	}
	res = h.do(t, req{
		method: "POST", path: "/api/v1/nodes/" + nodeA + "/debits", body: `{"amount":"1"}`,
		header: withJSON(map[string]string{"Idempotency-Key": "op-1"}),
	})
	require.Equal(t, http.StatusNotImplemented, res.Code)
}

func TestRateLimitedAnswer(t *testing.T) {
	h := newHarness(t, nil)
	h.limiter.deny, h.limiter.retry = true, 7500*time.Millisecond
	res := h.do(t, req{method: "GET", path: "/api/v1/me", header: authed(nil)})
	require.Equal(t, http.StatusTooManyRequests, res.Code)
	require.Equal(t, "8", res.Header().Get("Retry-After"), "seconds, rounded up")
	require.Equal(t, "rate_limited", res.problem(t).Code)
	require.Contains(t, h.limiter.keys[0], "read:user:"+testActor.String())

	h.limiter.keys = nil
	h.do(t, req{method: "POST", path: "/api/v1/nodes/" + nodeA + "/deactivate", header: authed(nil)})
	require.Contains(t, h.limiter.keys[0], "mutation:user:"+testActor.String())
}

func TestLoginRateKeysUseNormalizedEmailAndAddress(t *testing.T) {
	h := newHarness(t, nil)
	login := func(email string) []string {
		h.limiter.keys = nil
		h.do(t, req{method: "POST", path: "/api/v1/auth/login", header: jsonHeader, body: `{"email":"` + email + `","password":"p"}`})
		return append([]string(nil), h.limiter.keys...)
	}
	a := login("User@Example.com")
	b := login("  user@example.COM ")
	c := login("other@example.com")
	require.Len(t, a, 2)
	require.Equal(t, a, b, "the key uses the normalized email")
	require.Equal(t, a[0], c[0], "the address bucket does not depend on the email")
	require.NotEqual(t, a[1], c[1])
	require.Equal(t, "login:ip:192.0.2.10", a[0])
	require.NotContains(t, a[1], "example.com", "the email is hashed in the key")
}

// Test-ID: T-U-67
func TestAccessLogHasNoSecrets(t *testing.T) {
	h := newHarness(t, nil)
	h.do(t, req{
		method: "POST", path: "/api/v1/auth/login", header: jsonHeader,
		body: `{"email":"victim@example.com","password":"Sup3rSecretPassw0rd"}`,
	})
	h.do(t, req{
		method: "GET", path: "/api/v1/me?cursor=abc",
		header: map[string]string{"Authorization": "Bearer eyJhbGciOiJFZERTQSJ9.payload.sig-secret", "X-Request-Id": "req-log-0001"},
	})
	h.do(t, req{method: "GET", path: "/api/v1/nodes/" + nodeA, header: authed(nil)})
	out := h.log.String()
	for _, secret := range []string{"Sup3rSecretPassw0rd", "victim@example.com", "eyJhbGci", "sig-secret", "Bearer", "Authorization", "good-token", "cursor=abc"} {
		require.NotContains(t, out, secret)
	}
	require.Contains(t, out, `"method":"GET"`)
	require.Contains(t, out, `"route":"GET /api/v1/nodes/{id}"`)
	require.Contains(t, out, `"status":401`)
	require.Contains(t, out, `"request_id":"req-log-0001"`)
	require.Contains(t, out, `"duration_ms"`)
	require.NotContains(t, out, nodeA, "the template is logged, not the concrete path")
}

func TestNewServerValidatesOptions(t *testing.T) {
	_, err := httpapi.NewServer(httpapi.Options{})
	require.Error(t, err)
}

// Test-ID: T-U-69
// TestEveryOperationExceptPublicRequiresBearer walks every operation of the
// OpenAPI contract and sends it without a token. Only the explicit public
// operations may answer anything but 401 token_missing: a new route is closed
// by default, and widening the public set must be a deliberate edit here.
func TestEveryOperationExceptPublicRequiresBearer(t *testing.T) {
	h := newHarness(t, fakeHandlers{})
	public := map[string]bool{
		"POST /auth/login":   true,
		"POST /auth/refresh": true, // refresh is cookie plus CSRF, checked by its handler
		"GET /healthz":       true,
		"GET /readyz":        true,
	}
	const id = "0190a0b0-0000-7000-8000-000000000001"
	idParam := regexp.MustCompile(`\{[^}]+\}`)

	ops := operations(loadSpec(t))
	require.GreaterOrEqual(t, len(ops), 30, "the walk must not be vacuous")

	seenPublic := 0
	for key, o := range ops {
		path := idParam.ReplaceAllString(o.path, id)
		if !strings.HasPrefix(path, "/healthz") && !strings.HasPrefix(path, "/readyz") {
			path = "/api/v1" + path
		}
		res := h.do(t, req{method: o.method, path: path})
		if public[key] {
			seenPublic++
			require.NotEqual(t, http.StatusUnauthorized, res.Code, key)
			continue
		}
		require.Equal(t, http.StatusUnauthorized, res.Code, key)
		require.Equal(t, "token_missing", res.problem(t).Code, key)
	}
	require.Equal(t, len(public), seenPublic, "every public operation exists in the contract")
}
