package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/Gwakko/tenant-backoffice/backend/internal/adapter/httpapi/gen"
	"github.com/Gwakko/tenant-backoffice/backend/internal/domain/apperr"
)

const (
	problemTypeBase    = "https://tenant-backoffice.invalid/problems/"
	problemContentType = "application/problem+json"
)

// codeNotImplemented answers the operations whose handlers are not written
// yet. It is a transport placeholder, not a domain code, and is not part of the
// OpenAPI ErrorCode enum.
const codeNotImplemented apperr.Code = "not_implemented"

// errNotImplemented is returned by the placeholder server.
var errNotImplemented = apperr.New(codeNotImplemented, "operation is not implemented")

type problemSpec struct {
	status        int
	title, detail string
}

// problemTable is the only place that maps apperr.Code to an HTTP status and
// to the text of the answer (spec 03 section 2.3). The detail is a fixed text
// per code and never comes from the error: an internal message, an identifier
// or a database error cannot reach the client, and two answers with the same
// code are identical (404 for a missing and for a foreign object, spec 02
// section 6).
var problemTable = map[apperr.Code]problemSpec{
	apperr.ValidationFailed:       {http.StatusBadRequest, "Validation failed", "One or more fields are invalid."},
	apperr.InvalidID:              {http.StatusBadRequest, "Invalid identifier", "An identifier is not a valid UUID."},
	apperr.InvalidAmount:          {http.StatusBadRequest, "Invalid amount", "The amount must be a string of digits within the allowed range."},
	apperr.AmountMustBePositive:   {http.StatusBadRequest, "Amount must be positive", "The amount must be greater than zero."},
	apperr.InvalidCursor:          {http.StatusBadRequest, "Invalid cursor", "The cursor is not valid for this request."},
	apperr.InvalidPeriod:          {http.StatusBadRequest, "Invalid period", "The report period or its parameters are not valid."},
	apperr.SameNode:               {http.StatusBadRequest, "Same node", "The source and the destination are the same node."},
	apperr.InvalidPassword:        {http.StatusBadRequest, "Invalid password", "The password does not satisfy the rules."},
	apperr.InvalidNodeType:        {http.StatusBadRequest, "Invalid node type", "The node type is not allowed."},
	apperr.InvalidName:            {http.StatusBadRequest, "Invalid name", "The name is not valid."},
	apperr.InvalidEmail:           {http.StatusBadRequest, "Invalid email", "The email is not valid."},
	apperr.InvalidRole:            {http.StatusBadRequest, "Invalid role", "The role is not known."},
	apperr.IdempotencyKeyRequired: {http.StatusBadRequest, "Idempotency key required", "The Idempotency-Key header is required."},
	apperr.IdempotencyKeyInvalid:  {http.StatusBadRequest, "Invalid idempotency key", "The Idempotency-Key header is not valid."},
	apperr.InvalidCredentials:     {http.StatusUnauthorized, "Invalid credentials", "The email or password is incorrect."},
	apperr.TokenMissing:           {http.StatusUnauthorized, "Token missing", "An access token is required."},
	apperr.TokenExpired:           {http.StatusUnauthorized, "Token expired", "The access token has expired."},
	apperr.TokenInvalid:           {http.StatusUnauthorized, "Token invalid", "The access token is not valid."},
	apperr.RefreshInvalid:         {http.StatusUnauthorized, "Refresh token invalid", "The refresh token is not valid."},
	apperr.Forbidden:              {http.StatusForbidden, "Forbidden", "You do not have permission to perform this action."},
	apperr.RoleNotGrantable:       {http.StatusForbidden, "Role not grantable", "You cannot grant or revoke a role higher than your own."},
	apperr.NotFound:               {http.StatusNotFound, "Not found", "The object was not found."},
	apperr.UnknownSetting:         {http.StatusNotFound, "Unknown setting", "The setting is not known."},
	apperr.NameTaken:              {http.StatusConflict, "Name taken", "The name is already used under this parent."},
	apperr.NodeInactive:           {http.StatusConflict, "Node inactive", "The operation is not allowed on an inactive node."},
	apperr.OperationsDisabled:     {http.StatusConflict, "Operations disabled", "Balance operations are disabled for this node."},
	apperr.InsufficientFunds:      {http.StatusConflict, "Insufficient funds", "The balance is not sufficient."},
	apperr.BalanceOverflow:        {http.StatusConflict, "Balance overflow", "The balance would exceed the maximum."},
	apperr.NodeHasChildren:        {http.StatusConflict, "Node has children", "The node has children and cannot be deleted."},
	apperr.NodeHasBalance:         {http.StatusConflict, "Node has balance", "The node has a balance and cannot be deleted."},
	apperr.NodeHasHistory:         {http.StatusConflict, "Node has history", "The node has operations and cannot be deleted."},
	apperr.ChildrenLimit:          {http.StatusConflict, "Children limit", "The parent has reached the maximum number of children."},
	apperr.LastRootOwner:          {http.StatusConflict, "Last root owner", "The last owner of the root cannot be removed or demoted."},
	apperr.UserInactive:           {http.StatusConflict, "User inactive", "The user is inactive."},
	apperr.InvalidParentType:      {http.StatusUnprocessableEntity, "Invalid parent type", "The parent type is not allowed for this node type."},
	apperr.MoveCycle:              {http.StatusUnprocessableEntity, "Move cycle", "A node cannot be moved into itself or its descendant."},
	apperr.RootImmutable:          {http.StatusUnprocessableEntity, "Root immutable", "The root node cannot be moved or deleted."},
	apperr.InvalidSettingValue:    {http.StatusUnprocessableEntity, "Invalid setting value", "The setting value is not valid."},
	apperr.AmountExceedsLimit:     {http.StatusUnprocessableEntity, "Amount exceeds limit", "The amount exceeds the allowed maximum."},
	apperr.IdempotencyKeyReused:   {http.StatusUnprocessableEntity, "Idempotency key reused", "The Idempotency-Key was already used for another request."},
	apperr.PayloadTooLarge:        {http.StatusRequestEntityTooLarge, "Payload too large", "The request body is too large."},
	apperr.UnsupportedMediaType:   {http.StatusUnsupportedMediaType, "Unsupported media type", "The request body must be application/json."},
	apperr.RateLimited:            {http.StatusTooManyRequests, "Rate limited", "Too many requests. Retry later."},
	apperr.Internal:               {http.StatusInternalServerError, "Internal error", "An internal error occurred."},
	apperr.Unavailable:            {http.StatusServiceUnavailable, "Service unavailable", "The service is temporarily unavailable."},
	codeNotImplemented:            {http.StatusNotImplemented, "Not implemented", "The operation is not implemented."},
}

// FieldProblem is one entry of errors[] (400/422 with field errors).
type FieldProblem struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Problem is the application/problem+json body (RFC 9457, spec 03 section 2.1).
type Problem struct {
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Status    int            `json:"status"`
	Code      string         `json:"code"`
	Detail    string         `json:"detail"`
	Errors    []FieldProblem `json:"errors,omitempty"`
	RequestID string         `json:"requestId"`
}

type resolved struct {
	code   apperr.Code
	spec   problemSpec
	fields []FieldProblem
	// cause is what goes to the log when the answer hides it.
	cause error
}

// resolve maps any error to the answer. Unknown errors, and apperr codes that
// have no row in the table, become 500 internal.
func resolve(err error) resolved {
	var ae apperr.Error
	if errors.As(err, &ae) {
		if spec, ok := problemTable[ae.Code]; ok {
			return resolved{code: ae.Code, spec: spec, fields: fieldProblems(ae), cause: err}
		}
		return internalResolved(err)
	}
	if r, ok := resolveParamError(err); ok {
		return r
	}
	return internalResolved(err)
}

func internalResolved(err error) resolved {
	return resolved{code: apperr.Internal, spec: problemTable[apperr.Internal], cause: err}
}

// fieldProblems lists the per-field details of a validation error, sorted by
// field. The field detail is the short token chosen by the domain (for example
// "required"); the code is the code of the error itself.
func fieldProblems(e apperr.Error) []FieldProblem {
	if len(e.Fields) == 0 {
		return nil
	}
	out := make([]FieldProblem, 0, len(e.Fields))
	for field, detail := range e.Fields {
		out = append(out, FieldProblem{Field: field, Code: string(e.Code), Message: detail})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// resolveParamError maps the errors of the generated parameter binding.
func resolveParamError(err error) (resolved, bool) {
	var (
		format   *gen.InvalidParamFormatError
		required *gen.RequiredParamError
		header   *gen.RequiredHeaderError
		unmarsh  *gen.UnmarshalingParamError
		many     *gen.TooManyValuesForParamError
	)
	switch {
	case errors.As(err, &format):
		if isIDParam(format.ParamName) {
			return fromAppErr(apperr.New(apperr.InvalidID, "identifier is not a UUID").WithField(format.ParamName, "invalid_uuid")), true
		}
		return fromAppErr(validationField(format.ParamName, "invalid")), true
	case errors.As(err, &required):
		return fromAppErr(validationField(required.ParamName, "required")), true
	case errors.As(err, &header):
		if header.ParamName == IdempotencyKeyHeader {
			return fromAppErr(apperr.New(apperr.IdempotencyKeyRequired, "header is required")), true
		}
		return fromAppErr(validationField(header.ParamName, "required")), true
	case errors.As(err, &unmarsh):
		return fromAppErr(validationField(unmarsh.ParamName, "invalid")), true
	case errors.As(err, &many):
		return fromAppErr(validationField(many.ParamName, "invalid")), true
	}
	return resolved{}, false
}

func fromAppErr(e apperr.Error) resolved {
	return resolved{code: e.Code, spec: problemTable[e.Code], fields: fieldProblems(e)}
}

func validationField(field, detail string) apperr.Error {
	return apperr.New(apperr.ValidationFailed, "invalid parameter").WithField(field, detail)
}

// isIDParam reports whether a parameter carries a UUID (path ids and id filters).
func isIDParam(name string) bool {
	return name == "id" || strings.HasSuffix(name, "Id")
}

// writeProblem answers a request with the problem for err. Internal failures
// and 5xx are logged with the request id; the body never carries their text.
func writeProblem(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	res := resolve(err)
	st := stateOf(r.Context())
	if res.spec.status >= http.StatusInternalServerError && res.code != codeNotImplemented {
		log.LogAttrs(r.Context(), slog.LevelError, "request failed",
			slog.String("request_id", st.requestID),
			slog.String("route", st.routeOrUnmatched()),
			slog.String("code", string(res.code)),
			slog.Any("error", res.cause))
	}
	if status := res.spec.status; status >= http.StatusBadRequest && status < http.StatusInternalServerError {
		// A refused request is logged with its code and request id, never
		// audited (EC-99). The body of a 404 cannot tell a foreign node from a
		// missing one; the log states only that.
		attrs := []slog.Attr{
			slog.String("request_id", st.requestID),
			slog.String("route", st.routeOrUnmatched()),
			slog.String("code", string(res.code)),
			slog.Int("status", status),
		}
		if status == http.StatusNotFound {
			attrs = append(attrs, slog.String("reason", "missing_or_outside_visible_subtree"))
		}
		log.LogAttrs(r.Context(), slog.LevelInfo, "request refused", attrs...)
	}
	body := Problem{
		Type:      problemTypeBase + string(res.code),
		Title:     res.spec.title,
		Status:    res.spec.status,
		Code:      string(res.code),
		Detail:    res.spec.detail,
		Errors:    res.fields,
		RequestID: st.requestID,
	}
	payload, mErr := json.Marshal(body)
	if mErr != nil { // not reachable: Problem has only plain fields
		payload = []byte(`{"code":"internal","status":500}`)
		res.spec.status = http.StatusInternalServerError
	}
	h := w.Header()
	h.Set("Content-Type", problemContentType)
	if res.code == apperr.TokenMissing || res.code == apperr.TokenInvalid || res.code == apperr.TokenExpired {
		h.Set("WWW-Authenticate", "Bearer")
	}
	w.WriteHeader(res.spec.status)
	_, _ = w.Write(payload)
}

// problemWriter returns the error handlers of the generated strict server.
func problemWriter(log *slog.Logger) gen.StrictHTTPServerOptions {
	return gen.StrictHTTPServerOptions{
		// A request that the generated code could not bind is the client's
		// mistake, whatever the cause: an unknown error is a validation error.
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			if _, ok := resolveParamError(err); !ok && !isAppErr(err) {
				err = apperr.New(apperr.ValidationFailed, "request is not valid")
			}
			writeProblem(w, r, log, err)
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, r, log, err)
		},
	}
}

func isAppErr(err error) bool {
	var ae apperr.Error
	return errors.As(err, &ae)
}

// notFoundHandler answers a path that no route matches.
func notFoundHandler(log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, log, apperr.NotFoundError("resource"))
	})
}

// StatusOf returns the HTTP status that the error mapping gives a code.
func StatusOf(code apperr.Code) (int, bool) {
	spec, ok := problemTable[code]
	return spec.status, ok
}
