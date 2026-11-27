// Package apitest is the client of the API tests (spec 08 section 3.4): login,
// bearer token, Idempotency-Key and decoding of problem+json. It has no
// knowledge of business handlers; the test files of the T-A suite build on it.
package apitest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
)

// Client talks to a running API (usually an httptest.Server). It is a value
// that is copied by WithToken, so one logged-in client per actor can coexist.
type Client struct {
	t       testing.TB
	baseURL string
	http    *http.Client
	token   string
}

// New builds a client for baseURL (without a trailing slash).
func New(t testing.TB, baseURL string) *Client {
	t.Helper()
	return &Client{t: t, baseURL: baseURL, http: &http.Client{
		// A redirect is a result worth asserting on, not something to follow.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// NewForHandler serves h on an httptest.Server that is closed with the test.
func NewForHandler(t testing.TB, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(t, srv.URL)
}

// WithToken returns a copy that sends the bearer token.
func (c *Client) WithToken(token string) *Client {
	cp := *c
	cp.token = token
	return &cp
}

// Token is the current bearer token.
func (c *Client) Token() string { return c.token }

// Login signs in and returns a copy of the client that carries the access
// token. It fails the test when the login is not 200.
func (c *Client) Login(email, password string) *Client {
	c.t.Helper()
	res := c.Do(http.MethodPost, "/api/v1/auth/login", gen.LoginRequest{Email: gen.Email(email), Password: password})
	require.Equal(c.t, http.StatusOK, res.Status, "login failed: %s", res.Body)
	var tr gen.TokenResponse
	res.JSON(&tr)
	require.NotEmpty(c.t, tr.AccessToken)
	return c.WithToken(tr.AccessToken)
}

// Option changes one request.
type Option func(*http.Request)

// WithIdempotencyKey sets the Idempotency-Key header.
func WithIdempotencyKey(key string) Option {
	return func(r *http.Request) { r.Header.Set(httpapi.IdempotencyKeyHeader, key) }
}

// WithHeader sets a header ("" removes it).
func WithHeader(name, value string) Option {
	return func(r *http.Request) {
		if value == "" {
			r.Header.Del(name)
			return
		}
		r.Header.Set(name, value)
	}
}

// WithCookie sends a cookie (the client has no jar: refresh tests pass the
// value they took from a Set-Cookie header themselves).
func WithCookie(name, value string) Option {
	return func(r *http.Request) {
		//nolint:gosec // a request cookie: the attributes of a Set-Cookie do not apply
		r.AddCookie(&http.Cookie{Name: name, Value: value})
	}
}

// WithCSRF sets the X-TB-CSRF header of the refresh cookie flow.
func WithCSRF() Option { return WithHeader(httpapi.CSRFHeader, httpapi.CSRFValue) }

// WithRequestID sets X-Request-Id.
func WithRequestID(id string) Option { return WithHeader(httpapi.RequestIDHeader, id) }

// Do sends a request. body is nil, a string or []byte (sent as is), or any
// value that is marshaled as JSON.
func (c *Client) Do(method, path string, body any, opts ...Option) *Response {
	c.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = bytes.NewReader([]byte(b))
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(body)
		require.NoError(c.t, err)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.baseURL+path, rd)
	require.NoError(c.t, err)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for _, o := range opts {
		o(req)
	}
	resp, err := c.http.Do(req)
	require.NoError(c.t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	return &Response{t: c.t, Status: resp.StatusCode, Header: resp.Header, Body: data}
}

// Get, Post, Put, Patch and Delete are shortcuts of Do.
func (c *Client) Get(path string, opts ...Option) *Response {
	c.t.Helper()
	return c.Do(http.MethodGet, path, nil, opts...)
}

func (c *Client) Post(path string, body any, opts ...Option) *Response {
	c.t.Helper()
	return c.Do(http.MethodPost, path, body, opts...)
}

func (c *Client) Put(path string, body any, opts ...Option) *Response {
	c.t.Helper()
	return c.Do(http.MethodPut, path, body, opts...)
}

func (c *Client) Patch(path string, body any, opts ...Option) *Response {
	c.t.Helper()
	return c.Do(http.MethodPatch, path, body, opts...)
}

func (c *Client) Delete(path string, opts ...Option) *Response {
	c.t.Helper()
	return c.Do(http.MethodDelete, path, nil, opts...)
}

// Response is a fully read answer.
type Response struct {
	t      testing.TB
	Status int
	Header http.Header
	Body   []byte
}

// Cookie returns the Set-Cookie of the answer with this name, or nil.
func (r *Response) Cookie(name string) *http.Cookie {
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// JSON decodes the body into v.
func (r *Response) JSON(v any) {
	r.t.Helper()
	require.NoError(r.t, json.Unmarshal(r.Body, v), "body: %s", r.Body)
}

// Problem decodes an application/problem+json answer.
func (r *Response) Problem() httpapi.Problem {
	r.t.Helper()
	require.Equal(r.t, "application/problem+json", r.Header.Get("Content-Type"), "body: %s", r.Body)
	var p httpapi.Problem
	r.JSON(&p)
	require.Equal(r.t, r.Status, p.Status, "problem status differs from the HTTP status")
	require.Equal(r.t, r.Header.Get(httpapi.RequestIDHeader), p.RequestID, "problem requestId differs from X-Request-Id")
	return p
}

// RequireProblem asserts the status and the code of a problem answer.
func (r *Response) RequireProblem(status int, code string) httpapi.Problem {
	r.t.Helper()
	require.Equal(r.t, status, r.Status, "body: %s", r.Body)
	p := r.Problem()
	require.Equal(r.t, code, p.Code)
	return p
}

var requestIDField = regexp.MustCompile(`"requestId":"[^"]*"`)

// BodyWithoutRequestID is the body with the request id blanked, for the
// byte-for-byte comparison of 404 answers (spec 02 section 6).
func (r *Response) BodyWithoutRequestID() string {
	return requestIDField.ReplaceAllString(string(r.Body), `"requestId":""`)
}
