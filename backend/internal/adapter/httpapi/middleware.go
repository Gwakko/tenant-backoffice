package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/user"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/auth"
	"github.com/Gwakko/tenant-backoffice/backend/internal/usecase/port"
)

// RequestIDHeader carries the request id in both directions.
const RequestIDHeader = "X-Request-Id"

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// Authenticator checks the access token of a request against the database
// (auth.AuthenticateUseCase implements it).
type Authenticator interface {
	Execute(ctx context.Context, in auth.AuthenticateInput) (auth.AuthenticateOutput, error)
}

// Limiter is the rate limiter port: limit requests per per for key.
type Limiter interface {
	Allow(key string, limit int, per time.Duration) (allowed bool, retryAfter time.Duration)
}

// RateBudgets are requests per minute (spec 02 section 7).
type RateBudgets struct {
	LoginPair, LoginIP, RefreshIP, Mutation, Read int
}

// DefaultRateBudgets are the budgets of the spec.
func DefaultRateBudgets() RateBudgets {
	return RateBudgets{LoginPair: 5, LoginIP: 20, RefreshIP: 20, Mutation: 60, Read: 600}
}

// reqState is the per-request record shared by the middleware. It is created by
// requestIDMiddleware and used by one goroutine only.
type reqState struct {
	requestID string
	route     string
	actor     user.ActorID
	hasActor  bool
	// client is what the handlers need from the raw request (see capture).
	client ClientInfo
}

func (s *reqState) routeOrUnmatched() string {
	if s.route == "" || s.route == "/" {
		return "unmatched"
	}
	return s.route
}

type ctxKey int

const (
	stateKey ctxKey = iota
	bodyKey
)

func stateOf(ctx context.Context) *reqState {
	if s, ok := ctx.Value(stateKey).(*reqState); ok {
		return s
	}
	return &reqState{}
}

// RequestIDFrom returns the id of the request.
func RequestIDFrom(ctx context.Context) string { return stateOf(ctx).requestID }

// ActorFrom returns the authenticated user of the request.
func ActorFrom(ctx context.Context) (user.ActorID, bool) {
	s := stateOf(ctx)
	return s.actor, s.hasActor
}

// bodyBytes is the buffered request body (set by limitBody).
func bodyBytes(ctx context.Context) []byte {
	b, _ := ctx.Value(bodyKey).([]byte)
	return b
}

type middleware func(http.Handler) http.Handler

func chain(h http.Handler, mws ...middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// recorder remembers the status and whether the header was sent.
type recorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(p []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// requestIDMiddleware accepts an incoming id only when it matches the format
// of the contract (spec 03 section 1), else generates one, and sets the
// response header before anything else can answer.
func requestIDMiddleware(newID func() string) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !requestIDPattern.MatchString(id) {
				id = newID()
			}
			w.Header().Set(RequestIDHeader, id)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-store")
			ctx := context.WithValue(r.Context(), stateKey, &reqState{requestID: id})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// accessLog writes one line per request: method, route template, status,
// duration and request id. It never logs the body, the query, headers or
// tokens.
func accessLog(log *slog.Logger, clock port.Clock) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := clock.Now()
			rec := &recorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			st := stateOf(r.Context())
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("route", st.routeOrUnmatched()),
				slog.Int("status", rec.status),
				slog.Int64("duration_ms", clock.Now().Sub(start).Milliseconds()),
				slog.String("request_id", st.requestID),
			}
			if st.hasActor {
				attrs = append(attrs, slog.String("actor_id", st.actor.String()))
			}
			log.LogAttrs(r.Context(), slog.LevelInfo, "http request", attrs...)
		})
	}
}

// recoverer turns a panic into 500 internal without details; the panic and the
// stack go to the log with the request id.
func recoverer(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					handlePanic(w, r, log, rec)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func handlePanic(w http.ResponseWriter, r *http.Request, log *slog.Logger, rec any) {
	if rec == http.ErrAbortHandler { //nolint:errorlint // the sentinel is compared by identity on purpose
		panic(rec)
	}
	st := stateOf(r.Context())
	log.LogAttrs(r.Context(), slog.LevelError, "panic in handler",
		slog.String("request_id", st.requestID),
		slog.String("route", st.routeOrUnmatched()),
		slog.String("panic", fmt.Sprint(rec)),
		slog.String("stack", string(debug.Stack())))
	if sent, ok := w.(*recorder); ok && sent.wrote {
		return // the answer has started; nothing sane can be added
	}
	writeProblem(w, r, log, apperr.InternalError())
}

// routing finds the route of the request before any decision depends on it and
// records its template. Middleware that must know the operation (authn, rate
// limit) therefore run in front of the router, not inside it.
func routing(mux *http.ServeMux) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, pattern := mux.Handler(r)
			stateOf(r.Context()).route = pattern
			next.ServeHTTP(w, r)
		})
	}
}

// limitBody reads the body (at most max bytes) into memory: 413 when it is
// larger. Later middleware and the router read the buffered copy.
func limitBody(maxBytes int64, log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				writeProblem(w, r, log, apperr.PayloadTooLargeError())
				return
			}
			if r.Body == nil || r.Body == http.NoBody {
				next.ServeHTTP(w, r)
				return
			}
			data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
			var tooBig *http.MaxBytesError
			switch {
			case errors.As(err, &tooBig):
				writeProblem(w, r, log, apperr.PayloadTooLargeError())
				return
			case err != nil:
				writeProblem(w, r, log, apperr.New(apperr.ValidationFailed, "body could not be read").WithField("body", "unreadable"))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(data))
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), bodyKey, data)))
		})
	}
}

// requireJSON answers 415 to a request that has a body that is not
// application/json (charset, when present, must be utf-8).
func requireJSON(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(bodyBytes(r.Context())) > 0 && !isJSONContentType(r.Header.Get("Content-Type")) {
				writeProblem(w, r, log, apperr.UnsupportedMediaTypeError())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isJSONContentType(v string) bool {
	media, params, err := mime.ParseMediaType(v)
	if err != nil || media != "application/json" {
		return false
	}
	cs, has := params["charset"]
	return !has || strings.EqualFold(cs, "utf-8")
}

const apiPrefix = "/api/v1"

// Routes that need no bearer token. Every other route under /api/v1 does.
var publicRoutes = map[string]bool{
	"POST " + apiPrefix + "/auth/login":   true,
	"POST " + apiPrefix + "/auth/refresh": true,
}

func needsAuth(pattern string) bool {
	_, path, ok := strings.Cut(pattern, " ")
	return ok && strings.HasPrefix(path, apiPrefix+"/") && !publicRoutes[pattern]
}

// authn checks the bearer token and puts the actor into the request. The
// token is verified and the user is read from the database by the use case
// (state and token_version); no right is read from the token (spec 02 section 7).
func authn(a Authenticator, log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			st := stateOf(r.Context())
			if !needsAuth(st.route) {
				next.ServeHTTP(w, r)
				return
			}
			token, err := bearerToken(r.Header)
			if err != nil {
				writeProblem(w, r, log, err)
				return
			}
			out, err := a.Execute(r.Context(), auth.AuthenticateInput{AccessToken: token})
			if err != nil {
				writeProblem(w, r, log, err)
				return
			}
			st.actor, st.hasActor = out.Actor, true
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(h http.Header) (string, error) {
	vals := h.Values("Authorization")
	if len(vals) == 0 {
		return "", apperr.TokenMissingError()
	}
	if len(vals) > 1 {
		return "", apperr.TokenInvalidError()
	}
	scheme, token, ok := strings.Cut(vals[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t") {
		return "", apperr.TokenInvalidError()
	}
	return token, nil
}

// rateLimit applies the budgets: login by (client address, normalized email)
// and by client address, refresh by address, everything else by user (mutations
// and reads in separate buckets). A refused request gets 429 with Retry-After.
func rateLimit(l Limiter, b RateBudgets, trustXFF bool, log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			st := stateOf(r.Context())
			type check struct {
				key   string
				limit int
			}
			var checks []check
			switch {
			case st.route == "POST "+apiPrefix+"/auth/login":
				ip := clientIP(r, trustXFF)
				checks = []check{
					{"login:ip:" + ip, b.LoginIP},
					{"login:pair:" + ip + ":" + loginEmailKey(bodyBytes(r.Context())), b.LoginPair},
				}
			case st.route == "POST "+apiPrefix+"/auth/refresh":
				checks = []check{{"refresh:ip:" + clientIP(r, trustXFF), b.RefreshIP}}
			case st.hasActor && isMutation(r.Method):
				checks = []check{{"mutation:user:" + st.actor.String(), b.Mutation}}
			case st.hasActor:
				checks = []check{{"read:user:" + st.actor.String(), b.Read}}
			}
			for _, c := range checks {
				if ok, retry := l.Allow(c.key, c.limit, time.Minute); !ok {
					w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retry)))
					log.LogAttrs(r.Context(), slog.LevelWarn, "rate limited",
						slog.String("request_id", st.requestID), slog.String("route", st.routeOrUnmatched()))
					writeProblem(w, r, log, apperr.RateLimitedError())
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func retryAfterSeconds(d time.Duration) int {
	s := int((d + time.Second - 1) / time.Second)
	if s < 1 {
		return 1
	}
	return s
}

func isMutation(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// loginEmailKey is a bounded, normalized key of the email of a login body. A
// body that cannot be read still gets a key (the empty email), so garbage does
// not bypass the limit.
func loginEmailKey(body []byte) string {
	var in struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(body, &in)
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if e, err := user.ParseEmail(in.Email); err == nil {
		email = e.String()
	}
	sum := sha256.Sum256([]byte(email))
	return hex.EncodeToString(sum[:8])
}

// clientIP is the address the limits are counted by: the TCP peer, or with
// trustXFF the last X-Forwarded-For entry (the one the trusted proxy appended).
// An IPv6 address is reduced to its /64 so that one host cannot rotate within
// its prefix.
func clientIP(r *http.Request, trustXFF bool) string {
	raw := ""
	if trustXFF {
		if v := r.Header.Values("X-Forwarded-For"); len(v) > 0 {
			parts := strings.Split(v[len(v)-1], ",")
			raw = strings.TrimSpace(parts[len(parts)-1])
		}
	}
	if raw == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		raw = host
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return "unknown"
	}
	addr = addr.Unmap()
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}

// Operations of balance that need an Idempotency-Key.
var idempotentRoutes = map[string]bool{
	"POST " + apiPrefix + "/nodes/{id}/credits": true,
	"POST " + apiPrefix + "/nodes/{id}/debits":  true,
	"POST " + apiPrefix + "/transfers":          true,
}

// idempotencyKey validates the header of the balance operations before the
// request reaches the generated binding.
func idempotencyKey(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if idempotentRoutes[stateOf(r.Context()).route] {
				if _, err := ParseIdempotencyKey(r.Header); err != nil {
					writeProblem(w, r, log, err)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// jsonBodies lists the operations with a JSON body and the generated type of
// the body. The generated strict decoder ignores unknown fields; this check
// rejects them (EC-114) and maps a number in the amount to invalid_amount (EC-76).
var jsonBodies = map[string]reflect.Type{
	"POST " + apiPrefix + "/auth/login":                 reflect.TypeFor[gen.LoginJSONRequestBody](),
	"POST " + apiPrefix + "/auth/password":              reflect.TypeFor[gen.ChangePasswordJSONRequestBody](),
	"PATCH " + apiPrefix + "/nodes/{id}":                reflect.TypeFor[gen.RenameNodeJSONRequestBody](),
	"POST " + apiPrefix + "/nodes/{id}/children":        reflect.TypeFor[gen.CreateChildJSONRequestBody](),
	"POST " + apiPrefix + "/nodes/{id}/credits":         reflect.TypeFor[gen.CreditNodeJSONRequestBody](),
	"POST " + apiPrefix + "/nodes/{id}/debits":          reflect.TypeFor[gen.DebitNodeJSONRequestBody](),
	"POST " + apiPrefix + "/nodes/{id}/members":         reflect.TypeFor[gen.AddMemberJSONRequestBody](),
	"PUT " + apiPrefix + "/nodes/{id}/members/{userId}": reflect.TypeFor[gen.ChangeMemberRoleJSONRequestBody](),
	"POST " + apiPrefix + "/nodes/{id}/move":            reflect.TypeFor[gen.MoveNodeJSONRequestBody](),
	"PUT " + apiPrefix + "/nodes/{id}/settings/{key}":   reflect.TypeFor[gen.PutSettingJSONRequestBody](),
	"POST " + apiPrefix + "/transfers":                  reflect.TypeFor[gen.CreateTransferJSONRequestBody](),
}

func strictBody(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			typ, ok := jsonBodies[stateOf(r.Context()).route]
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			if err := checkBody(bodyBytes(r.Context()), typ); err != nil {
				writeProblem(w, r, log, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func checkBody(body []byte, typ reflect.Type) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return apperr.New(apperr.ValidationFailed, "body is required").WithField("body", "required")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(reflect.New(typ).Interface()); err != nil {
		return bodyError(err)
	}
	if dec.More() {
		return apperr.New(apperr.ValidationFailed, "trailing data").WithField("body", "invalid_json")
	}
	return nil
}

// bodyError maps a decoding error to a client error without echoing Go types
// or the content of the body.
func bodyError(err error) apperr.Error {
	var typeErr *json.UnmarshalTypeError
	var syntax *json.SyntaxError
	switch {
	case errors.As(err, &typeErr):
		field := typeErr.Field
		if field == "amount" || strings.HasSuffix(field, ".amount") {
			return apperr.New(apperr.InvalidAmount, "amount must be a string").WithField("amount", "invalid_type")
		}
		return apperr.New(apperr.ValidationFailed, "field has a wrong type").WithField(safeField(field), "invalid_type")
	case errors.As(err, &syntax), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return apperr.New(apperr.ValidationFailed, "body is not valid JSON").WithField("body", "invalid_json")
	}
	if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		name, uErr := strconv.Unquote(name)
		if uErr != nil {
			name = "body"
		}
		return apperr.New(apperr.ValidationFailed, "unknown field").WithField(safeField(name), "unknown_field")
	}
	return apperr.New(apperr.ValidationFailed, "body is not valid").WithField("body", "invalid")
}

// safeField bounds a field name taken from the request before it is echoed.
func safeField(s string) string {
	const maxField = 64
	if s == "" {
		return "body"
	}
	if len(s) > maxField {
		s = s[:maxField]
	}
	return strings.ToValidUTF8(s, "?")
}
