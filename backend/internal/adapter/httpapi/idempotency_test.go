package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

const (
	nodeA = "0190f3a0-0000-7000-8000-00000000000a"
	nodeB = "0190f3a0-0000-7000-8000-00000000000b"
)

func creditHash(t *testing.T, route, node, body string) []byte {
	t.Helper()
	h, err := httpapi.RequestHash(httpapi.Fingerprint{
		Method: "POST", Route: route, PathParams: map[string]string{"id": node}, Body: []byte(body),
	})
	require.NoError(t, err)
	require.Len(t, h, 32)
	return h
}

// Test-ID: T-U-21
func TestRequestHashIncludesRoute(t *testing.T) {
	body := `{"amount":"1050","comment":"top up"}`
	credit := creditHash(t, "/nodes/{id}/credits", nodeA, body)
	debit := creditHash(t, "/nodes/{id}/debits", nodeA, body)
	require.NotEqual(t, credit, debit, "the same body on another route is another request (EC-69)")

	// Same meaning, same hash: field order, spacing, leading zeros of the amount.
	require.Equal(t, credit, creditHash(t, "/nodes/{id}/credits", nodeA, `{ "comment": "top up",  "amount": "1050" }`))
	require.Equal(t, credit, creditHash(t, "/nodes/{id}/credits", nodeA, `{"amount":"001050","comment":"top up"}`))
	require.Equal(t, credit, creditHash(t, "/nodes/{id}/credits", nodeA, "{\n\t\"amount\": \"1050\",\n\t\"comment\": \"top up\"\n}"))

	// Any difference in the meaning gives another hash.
	require.NotEqual(t, credit, creditHash(t, "/nodes/{id}/credits", nodeA, `{"amount":"1051","comment":"top up"}`))
	require.NotEqual(t, credit, creditHash(t, "/nodes/{id}/credits", nodeB, body), "another node")
	require.NotEqual(t, credit, creditHash(t, "/nodes/{id}/credits", nodeA, `{"amount":"1050","comment":"top-up"}`))
	require.NotEqual(t, credit, creditHash(t, "/nodes/{id}/credits", nodeA, `{"amount":"1050","comment":"top up "}`))
	require.NotEqual(t, credit, creditHash(t, "/nodes/{id}/credits", nodeA, `{"amount":"1050"}`))
	require.NotEqual(t, credit, creditHash(t, "/nodes/{id}/credits", nodeA, `{"amount":"1050","comment":"top up","x":1}`))
}

func TestRequestHashDistinguishesAmountForms(t *testing.T) {
	a := creditHash(t, "/transfers", "", `{"amount":"10","to":"x"}`)
	require.NotEqual(t, a, creditHash(t, "/transfers", "", `{"amount":"100","to":"x"}`))
	require.NotEqual(t, a, creditHash(t, "/transfers", "", `{"amount":10,"to":"x"}`), "a JSON number is not the string 10")
	zero := creditHash(t, "/transfers", "", `{"amount":"0","to":"x"}`)
	require.Equal(t, zero, creditHash(t, "/transfers", "", `{"amount":"000","to":"x"}`))
}

func TestRequestHashMethodAndParamsAreDelimited(t *testing.T) {
	h1, err := httpapi.RequestHash(httpapi.Fingerprint{Method: "POST", Route: "/a", PathParams: map[string]string{"id": "x"}, Body: []byte(`{}`)})
	require.NoError(t, err)
	h2, err := httpapi.RequestHash(httpapi.Fingerprint{Method: "post", Route: "/a", PathParams: map[string]string{"id": "x"}, Body: []byte(`{}`)})
	require.NoError(t, err)
	require.Equal(t, h1, h2, "the method is case-insensitive")
	h3, err := httpapi.RequestHash(httpapi.Fingerprint{Method: "PUT", Route: "/a", PathParams: map[string]string{"id": "x"}, Body: []byte(`{}`)})
	require.NoError(t, err)
	require.NotEqual(t, h1, h3)

	_, err = httpapi.RequestHash(httpapi.Fingerprint{Method: "POST", Route: "/a", PathParams: map[string]string{"id": "x\ny"}})
	require.Error(t, err, "a separator inside a parameter would make the hash ambiguous")
	_, err = httpapi.RequestHash(httpapi.Fingerprint{Method: "POST", Route: "/a", Body: []byte(`{"a":`)})
	require.Error(t, err)
	_, err = httpapi.RequestHash(httpapi.Fingerprint{Method: "POST", Route: "/a", Body: []byte(`{} {}`)})
	require.Error(t, err)
}

func TestParseIdempotencyKey(t *testing.T) {
	header := func(vals ...string) http.Header {
		h := http.Header{}
		for _, v := range vals {
			h.Add("Idempotency-Key", v)
		}
		return h
	}
	k, err := httpapi.ParseIdempotencyKey(header("op_1-a:b.c"))
	require.NoError(t, err)
	require.Equal(t, "op_1-a:b.c", k)
	k, err = httpapi.ParseIdempotencyKey(header(strings.Repeat("a", 128)))
	require.NoError(t, err)
	require.Len(t, k, 128)

	_, err = httpapi.ParseIdempotencyKey(http.Header{})
	require.ErrorIs(t, err, apperr.New(apperr.IdempotencyKeyRequired, ""))

	for name, h := range map[string]http.Header{
		"empty":     header(""),
		"too long":  header(strings.Repeat("a", 129)),
		"space":     header("a b"),
		"slash":     header("a/b"),
		"non-ascii": header("ключ"),
		"two":       header("a", "b"),
	} {
		_, err := httpapi.ParseIdempotencyKey(h)
		require.ErrorIs(t, err, apperr.New(apperr.IdempotencyKeyInvalid, ""), name)
	}
}
