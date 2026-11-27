package apitest_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/apitest"
)

// stub plays the part of the server: login, an echo of the headers, a problem.
func stub() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Email, Password string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Password != "right" {
			w.Header().Set("X-Request-Id", "req-0000001")
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"invalid_credentials","status":401,"requestId":"req-0000001"}`))
			return
		}
		_, _ = w.Write([]byte(`{"accessToken":"tok-1","expiresIn":600,"user":{"id":"0190f3a0-0000-7000-8000-0000000000aa","email":"a@b.c"}}`))
	})
	mux.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"auth": r.Header.Get("Authorization"), "key": r.Header.Get("Idempotency-Key"), "rid": r.Header.Get("X-Request-Id"),
		})
	})
	return mux
}

func TestClientLoginAndHeaders(t *testing.T) {
	c := apitest.NewForHandler(t, stub())
	anon := c.Post("/echo", map[string]string{"a": "b"})
	var got map[string]string
	anon.JSON(&got)
	require.Empty(t, got["auth"])

	me := c.Login("a@b.c", "right")
	require.Equal(t, "tok-1", me.Token())
	require.Empty(t, c.Token(), "Login returns a copy")

	res := me.Post("/echo", nil, apitest.WithIdempotencyKey("key-1"), apitest.WithRequestID("my-request-id"))
	res.JSON(&got)
	require.Equal(t, "Bearer tok-1", got["auth"])
	require.Equal(t, "key-1", got["key"])
	require.Equal(t, "my-request-id", got["rid"])
}

func TestClientProblemDecoding(t *testing.T) {
	c := apitest.NewForHandler(t, stub())
	res := c.Post("/api/v1/auth/login", `{"email":"a@b.c","password":"wrong"}`)
	p := res.RequireProblem(http.StatusUnauthorized, "invalid_credentials")
	require.Equal(t, "req-0000001", p.RequestID)
	require.JSONEq(t, `{"code":"invalid_credentials","status":401,"requestId":""}`, res.BodyWithoutRequestID())
}
