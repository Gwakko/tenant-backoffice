package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

// IdempotencyKeyHeader is the header that makes balance operations repeatable.
const IdempotencyKeyHeader = "Idempotency-Key"

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_\-:.]{1,128}$`)

// ParseIdempotencyKey reads and validates the Idempotency-Key header: required,
// a single value, 1..128 characters of [A-Za-z0-9_\-:.].
func ParseIdempotencyKey(h http.Header) (string, error) {
	vals := h.Values(IdempotencyKeyHeader)
	switch {
	case len(vals) == 0:
		return "", apperr.New(apperr.IdempotencyKeyRequired, "Idempotency-Key header is required")
	case len(vals) > 1 || !idempotencyKeyPattern.MatchString(vals[0]):
		return "", apperr.New(apperr.IdempotencyKeyInvalid, "Idempotency-Key header is not valid")
	}
	return vals[0], nil
}

// Fingerprint identifies the meaning of a balance request (spec 03 section 4):
// the same meaning gives the same hash, any difference gives another one.
type Fingerprint struct {
	Method string
	// Route is the route template (for example "/nodes/{id}/credits"), so the
	// same body sent to another route is another request (EC-69).
	Route string
	// PathParams are the values of the template parameters (the node id).
	PathParams map[string]string
	// Body is the JSON body as received.
	Body []byte
}

// RequestHash returns sha256(method "\n" route "\n" path params "\n" canonical
// body): 32 bytes for balance.OperateInput.RequestHash. Canonical JSON has
// sorted keys, no whitespace and amounts as canonical digit strings, so field
// order and spacing do not matter.
func RequestHash(f Fingerprint) ([]byte, error) {
	names := make([]string, 0, len(f.PathParams))
	for k, v := range f.PathParams {
		if strings.ContainsAny(k+v, "\n=,") {
			return nil, errors.New("httpapi: path parameter has a reserved character")
		}
		names = append(names, k)
	}
	sort.Strings(names)
	params := make([]string, len(names))
	for i, k := range names {
		params[i] = k + "=" + f.PathParams[k]
	}
	canon, err := CanonicalJSON(f.Body)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write([]byte(strings.ToUpper(f.Method)))
	h.Write([]byte("\n"))
	h.Write([]byte(f.Route))
	h.Write([]byte("\n"))
	h.Write([]byte(strings.Join(params, ",")))
	h.Write([]byte("\n"))
	h.Write(canon)
	return h.Sum(nil), nil
}

// CanonicalJSON re-encodes a JSON document with sorted object keys and no
// whitespace. A string under the key "amount" made only of digits loses its
// leading zeros ("0100" and "100" are one amount); numbers keep their text.
func CanonicalJSON(body []byte) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonical json: %w", err)
	}
	if dec.More() {
		return nil, errors.New("canonical json: trailing data")
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(normalize(v, "")); err != nil {
		return nil, fmt.Errorf("canonical json: %w", err)
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

func normalize(v any, key string) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = normalize(e, k)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalize(e, key)
		}
		return out
	case string:
		if key == "amount" {
			return canonicalDigits(t)
		}
		return t
	default:
		return v
	}
}

func canonicalDigits(s string) string {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return s
	}
	if t := strings.TrimLeft(s, "0"); t != "" {
		return t
	}
	return "0"
}
