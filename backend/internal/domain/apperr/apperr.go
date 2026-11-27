// Package apperr defines the domain error type shared by all layers. Codes are
// stable identifiers; mapping a code to a transport status happens only in the
// HTTP adapter.
package apperr

import "errors"

// Code is a stable machine-readable error code.
type Code string

// Codes used by the node domain.
const (
	ValidationFailed  Code = "validation_failed"
	InvalidID         Code = "invalid_id"
	InvalidNodeType   Code = "invalid_node_type"
	InvalidName       Code = "invalid_name"
	InvalidParentType Code = "invalid_parent_type"
	MoveCycle         Code = "move_cycle"
	RootImmutable     Code = "root_immutable"
	NodeInactive      Code = "node_inactive"
	NodeHasChildren   Code = "node_has_children"
	NodeHasBalance    Code = "node_has_balance"
	NodeHasHistory    Code = "node_has_history"
	NameTaken         Code = "name_taken"
	ChildrenLimit     Code = "children_limit"
)

// Codes used by the role and user domains.
const (
	InvalidEmail     Code = "invalid_email"
	InvalidPassword  Code = "invalid_password"
	InvalidRole      Code = "invalid_role"
	RoleNotGrantable Code = "role_not_grantable"
)

// Codes of membership use cases.
const (
	LastRootOwner Code = "last_root_owner"
	UserInactive  Code = "user_inactive"
)

// Codes used by the money, balance, settings and audit domains.
const (
	InvalidAmount        Code = "invalid_amount"
	AmountMustBePositive Code = "amount_must_be_positive"
	BalanceOverflow      Code = "balance_overflow"
	InsufficientFunds    Code = "insufficient_funds"
	AmountExceedsLimit   Code = "amount_exceeds_limit"
	OperationsDisabled   Code = "operations_disabled"
	SameNode             Code = "same_node"
	UnknownSetting       Code = "unknown_setting"
	InvalidSettingValue  Code = "invalid_setting_value"
)

// Codes shared by every use case: absent or hidden objects, missing rights and
// an unavailable store.
const (
	NotFound    Code = "not_found"
	Forbidden   Code = "forbidden"
	Unavailable Code = "db_unavailable"
)

// Error is a domain error with a code, a human-readable message and optional
// per-field details.
type Error struct {
	Code    Code
	Message string
	Fields  map[string]string
}

// New builds an Error without field details.
func New(code Code, message string) Error {
	return Error{Code: code, Message: message}
}

// WithField returns a copy of e with one more field detail.
func (e Error) WithField(field, detail string) Error {
	fields := make(map[string]string, len(e.Fields)+1)
	for k, v := range e.Fields {
		fields[k] = v
	}
	fields[field] = detail
	e.Fields = fields
	return e
}

// Error implements the error interface.
func (e Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

// Is reports whether target is an Error with the same code, so that
// errors.Is(err, apperr.New(code, "")) matches by code only.
func (e Error) Is(target error) bool {
	t, ok := target.(Error)
	return ok && t.Code == e.Code
}

// CodeOf extracts the code of the first Error in err's chain.
func CodeOf(err error) (Code, bool) {
	var e Error
	if errors.As(err, &e) {
		return e.Code, true
	}
	return "", false
}

// NotFoundError is the single answer for an object that does not exist and for
// one that exists outside the actor's subtrees: the two must be
// indistinguishable, so the message names only the kind of object, never an id.
func NotFoundError(what string) Error {
	return New(NotFound, what+" not found")
}

// ForbiddenError means the object is visible to the actor but the role does not
// allow the action.
func ForbiddenError(message string) Error {
	return New(Forbidden, message)
}

// UnavailableError means the store could not complete the operation (for example
// a lost connection); the operation had no effect and may be retried.
func UnavailableError() Error {
	return New(Unavailable, "storage is temporarily unavailable")
}
